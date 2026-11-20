package webenv

import (
	"fmt"
	"strings"
)

const (
	PHPPoolLow  = "low"
	PHPPoolBusy = "busy"
)

func NormalizePHPPool(pool string) (string, error) {
	switch pool {
	case "":
		return PHPPoolBusy, nil
	case PHPPoolLow, PHPPoolBusy:
		return pool, nil
	default:
		return "", ErrAdvanced
	}
}

func PHPPoolPort(version, pool string) int {
	port := PHPPort(version)
	if pool == PHPPoolLow {
		return port
	}
	return port + 1000
}

func PHPPoolName(version, pool string) string {
	return "php" + strings.ReplaceAll(version, ".", "") + "_" + pool
}

type phpTemplatePool struct {
	Name    string
	Port    int
	P       FPMPool
	SlowLog bool
}

func phpTemplatePools(version string, config PHPConfig) []phpTemplatePool {
	low := config.FPM
	low.PM = "ondemand"
	return []phpTemplatePool{
		{Name: PHPPoolLow, Port: PHPPoolPort(version, PHPPoolLow), P: low, SlowLog: low.SlowlogTimeout > 0},
		{Name: PHPPoolBusy, Port: PHPPoolPort(version, PHPPoolBusy), P: config.FPM, SlowLog: config.FPM.SlowlogTimeout > 0},
	}
}

func suggestFPM(memMB, pools int) FPMPool {
	pools = max(pools, 1)
	n := min(max(memMB/2/40/pools, 5), 300)
	return FPMPool{PM: "dynamic", MaxChildren: n, StartServers: max(n/10, 2), MinSpare: max(n/10, 1), MaxSpare: max(n/4, 4), MaxRequests: 500}
}

func SuggestFPMShared(memMB, versions int) FPMPool {
	return suggestFPM(memMB, max(versions*2, 2))
}

func phpUpstreamName(version, pool string) string {
	return fmt.Sprintf("%s", PHPPoolName(version, pool))
}
