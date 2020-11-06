package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/go-github/v32/github"
	"golang.org/x/oauth2"
)

var (
	modules           = flag.String("modules", "", "Path to the a file listing all go module deps")
	githubToken       = flag.String("github_token", "", "Github OAuth Token")
	tryPkgDevGo       = flag.Bool("try_pkg_dev_go", false, "Whether to query pkg.dev.go for licenses")
	jsonManualInput   = flag.String("json_manual_input", "", "Path to input json file with manually fetched licenses")
	jsonOutput        = flag.String("json_output", "", "Path to output json file")
	jsonMissingOutput = flag.String("json_missing_output", "", "Path to output json file with missing licenses")
)

var remapRepos = map[string]string{
	"pixie-labs/aes-min":      "cmcqueen/aes-min",
	"pixie-labs/arrow":        "apache/arrow",
	"pixie-labs/bcc":          "iovisor/bcc",
	"pixie-labs/bpftrace":     "iovisor/bpftrace",
	"pixie-labs/cpplint":      "cpplint/cpplint",
	"pixie-labs/dnsparser":    "packetzero/dnsparser",
	"pixie-labs/ELFIO":        "serge1/ELFIO",
	"pixie-labs/grpc":         "grpc/grpc",
	"pixie-labs/kuberesolver": "sercand/kuberesolver",
	"pixie-labs/libpypa":      "vinzenz/libpypa",
	"pixie-labs/protobuf":     "protocolbuffers/protobuf",
	"pixie-labs/tdigest":      "derrickburns/tdigest",
	"pixie-labs/threadstacks": "thoughtspot/threadstacks",
}

// Keep this in sync with src/ui/tools/licenses/npm_license_extractor.py
type dependency struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Package     string `json:"-"`
	LicenseSPDX string `json:"spdxID,omitempty"`
	LicenseText string `json:"licenseText,omitempty"`
}

func readData(filename string) (map[string]*dependency, error) {
	licenseFile, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer licenseFile.Close()

	var deps []dependency
	dec := json.NewDecoder(licenseFile)
	err = dec.Decode(&deps)
	if err != nil && err != io.EOF {
		return nil, err
	}

	licenses := make(map[string]*dependency)
	for _, dep := range deps {
		licenses[dep.Name] = &dep
	}
	return licenses, nil
}

func writeData(dependencies []*dependency, filename string) error {
	outputFile, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer outputFile.Close()
	jsonStr, err := json.MarshalIndent(dependencies, "", "    ")
	if err != nil {
		return err
	}
	if _, err := outputFile.Write(jsonStr); err != nil {
		return err
	}
	return outputFile.Sync()
}

func getNameAndURL(pkg string) (string, string) {
	if *tryPkgDevGo && !strings.Contains(pkg, "github") {
		// This is a non github go pkg dep. Resolve to the go pkg manager
		// since these are usually URL like but not always valid pages on the internet.
		// e.g. k8s.io/klog
		return pkg, fmt.Sprintf("https://pkg.go.dev/%s", pkg)
	}

	if !strings.Contains(pkg, "github") {
		// At this point we better be in Github land else all assumptions break.
		return pkg, ""
	}

	// Handle submodules:
	// Go from git url to http url.
	pkg = strings.Replace(pkg, "git@github.com:", "github.com/", 1)
	// Nuke the .git suffix if any.
	pkg = strings.TrimSuffix(pkg, ".git")

	// Drop the scheme if any.
	if strings.Contains(pkg, "://") {
		parts := strings.Split(pkg, "://")
		pkg = parts[1]
	}

	// Drop anything besides the Username and Reponame.
	parts := strings.Split(pkg, "/")
	if len(parts) < 3 {
		// This dep is somehow underspecified?
		return pkg, ""
	}

	name := strings.Join(parts[1:3], "/")
	// Remap our forks to the orignal.
	if _, ok := remapRepos[name]; ok {
		name = remapRepos[name]
	}

	return name, fmt.Sprintf("https://github.com/%s", name)
}

func tryFetchGithubLicense(ctx context.Context, client *github.Client, dep *dependency) {
	if !strings.HasPrefix(dep.URL, "https://github.com") {
		// Can't fetch licenses from GitHub if this is not on GitHub.
		return
	}
	parts := strings.Split(dep.Name, "/")
	if len(parts) < 2 {
		return
	}
	repoLicense, _, err := client.Repositories.License(ctx, parts[0], parts[1])
	if err != nil {
		return
	}
	dep.LicenseSPDX = repoLicense.GetLicense().GetSPDXID()
	if dep.LicenseSPDX == "NOASSERTION" {
		// GitHub found some license text but didn't map it to a SPDX ID.
		// Let someone else figure this out.
		dep.LicenseSPDX = ""
	}
	// GitHub gives us base64 encoded licenses ... hooray.
	decodedLicense, err := base64.StdEncoding.DecodeString(repoLicense.GetContent())
	if err != nil {
		dep.LicenseText = repoLicense.GetContent()
	} else {
		dep.LicenseText = string(decodedLicense)
	}
}

func tryFetchPkgGoDevLicense(ctx context.Context, dep *dependency) {
	if !*tryPkgDevGo {
		// Must not be go deps.
		return
	}
	if dep.LicenseSPDX != "" {
		// We must have fetched from GitHub successfully, so don't do anything here.
		return
	}
	resp, err := http.Get(fmt.Sprintf("https://pkg.go.dev/%s?tab=licenses", dep.Package))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return
	}
	// Let's hope that the HTML structure of this page never changes ... because,
	// they somehow still don't have an API to access pkg.go.dev
	doc.Find(".License").Each(func(_ int, s *goquery.Selection) {
		if dep.LicenseSPDX != "" {
			return
		}
		dep.LicenseSPDX = s.Find("h2 div").Text()
		dep.LicenseText = s.Find(".License-contents").Text()
	})
}

func tryFetchJSONManualLicense(dep *dependency, manual map[string]*dependency) {
	if dep.LicenseSPDX != "" {
		// A previous fetcher was successful, so don't do anything here.
		return
	}
	found, ok := manual[dep.Name]
	if !ok {
		// Well, at this point someone needs to go add license info for this into
		// the manual_licenses.json file.
		return
	}
	dep.LicenseSPDX = found.LicenseSPDX
	dep.LicenseText = found.LicenseText
}

func main() {
	flag.Parse()
	ctx := context.Background()

	if *modules == "" {
		log.Fatal("Must specfy --modules")
	}

	if *jsonOutput == "" {
		log.Fatal("Must specfy --json_output")
	}

	var manual map[string]*dependency
	var err error
	if *jsonManualInput != "" {
		manual, err = readData(*jsonManualInput)
	}
	if err != nil {
		log.Fatal(err)
	}

	modulesFile, err := os.Open(*modules)
	if err != nil {
		log.Fatal(err)
	}
	defer modulesFile.Close()

	var deps []*dependency
	pkgSeen := make(map[string]bool)
	scanner := bufio.NewScanner(modulesFile)
	for scanner.Scan() {
		pkg := scanner.Text()
		name, url := getNameAndURL(scanner.Text())
		if !pkgSeen[name] {
			deps = append(deps, &dependency{Name: name, URL: url, Package: pkg})
		}
		pkgSeen[name] = true
	}
	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	var tc *http.Client
	if *githubToken != "" {
		tc = oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: *githubToken}))
	}
	client := github.NewClient(tc)

	var wg sync.WaitGroup
	work := make(chan *dependency)

	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(ctx context.Context, client *github.Client, manual map[string]*dependency) {
			defer wg.Done()
			for dep := range work {
				tryFetchGithubLicense(ctx, client, dep)
				tryFetchPkgGoDevLicense(ctx, dep)
				tryFetchJSONManualLicense(dep, manual)
			}
		}(ctx, client, manual)
	}

	for _, dep := range deps {
		work <- dep
	}

	close(work)
	wg.Wait()

	var found, missing []*dependency
	for _, dep := range deps {
		if dep.LicenseSPDX != "" {
			found = append(found, dep)
		} else {
			missing = append(missing, dep)
		}
	}

	err = writeData(found, *jsonOutput)
	if err != nil {
		log.Fatal(err)
	}
	if *jsonMissingOutput != "" {
		err = writeData(missing, *jsonMissingOutput)
		if err != nil {
			log.Fatal(err)
		}
	} else if len(missing) > 0 {
		log.Fatalf("There are %d repos with missing licenses and no path provided to write the missing licenses output", len(missing))
	}
}