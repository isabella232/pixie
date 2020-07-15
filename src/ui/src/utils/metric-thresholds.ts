const LATENCY_HIGH_THRESHOLD = 300;
const LATENCY_MED_THRESHOLD = 150;

export type GaugeLevel = 'low' | 'med' | 'high' | 'none';

export function getLatencyLevel(val: number): GaugeLevel {
  if (val < LATENCY_MED_THRESHOLD) {
    return 'low';
  }
  if (val < LATENCY_HIGH_THRESHOLD) {
    return 'med';
  }
  return 'high';
}

const CPU_HIGH_THRESHOLD = 80;
const CPU_MED_THRESHOLD = 70;

export function getCPULevel(val: number): GaugeLevel {
  if (val < CPU_MED_THRESHOLD) {
    return 'low';
  }
  if (val < CPU_HIGH_THRESHOLD) {
    return 'med';
  }
  return 'high';
}