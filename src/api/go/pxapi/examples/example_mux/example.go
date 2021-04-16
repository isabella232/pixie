package main

import (
	"context"
	"fmt"
	"os"

	"px.dev/pixie/src/api/go/pxapi"
	"px.dev/pixie/src/api/go/pxapi/errdefs"
	"px.dev/pixie/src/api/go/pxapi/errdefs/formatters"
	"px.dev/pixie/src/api/go/pxapi/muxes"
	"px.dev/pixie/src/api/go/pxapi/types"
)

var (
	pxl = `
import px
df = px.DataFrame('http_events')
df = df[['upid', 'req_path', 'remote_addr', 'req_method']]
df = df.head(10)
px.display(df, 'http_as_json')
px.display(df, 'http_as_table')
`
)

func main() {
	apiKey, ok := os.LookupEnv("PX_API_KEY")
	if !ok {
		panic("please set PX_API_KEY")
	}
	clusterID, ok := os.LookupEnv("PX_CLUSTER_ID")
	if !ok {
		panic("please set PX_CLUSTER_ID")
	}

	ctx := context.Background()
	client, err := pxapi.NewClient(ctx, pxapi.WithAPIKey(apiKey))
	if err != nil {
		panic(err)
	}

	fmt.Printf("Running on Cluster: %s\n", clusterID)

	tm := muxes.NewRegexTableMux()
	err = tm.RegisterHandlerForPattern("http_as_table", func(metadata types.TableMetadata) (pxapi.TableRecordHandler, error) {
		return formatters.NewTableFormatter(os.Stdout)
	})
	if err != nil {
		panic(err)
	}
	err = tm.RegisterHandlerForPattern("http_as_json", func(metadata types.TableMetadata) (pxapi.TableRecordHandler, error) {
		return formatters.NewJSONFormatter(os.Stdout)
	})
	if err != nil {
		panic(err)
	}
	vz, err := client.NewVizierClient(ctx, clusterID)
	if err != nil {
		panic(err)
	}

	resultSet, err := vz.ExecuteScript(ctx, pxl, tm)
	if err != nil {
		panic(err)
	}

	defer resultSet.Close()
	if err := resultSet.Stream(); err != nil {
		if errdefs.IsCompilationError(err) {
			fmt.Printf("Got compiler error: \n %s\n", err.Error())
		} else {
			fmt.Printf("Got error : %+v, while streaming\n", err)
		}
	}

	stats := resultSet.Stats()
	fmt.Printf("Execution Time: %v\n", stats.ExecutionTime)
	fmt.Printf("Bytes received: %v\n", stats.TotalBytes)
}