package facet

import "runtime"

// currentPlatform names this process's own platform the same way
// SetBundledSidecarSum's platform argument does: "<GOOS>-<GOARCH>".
func currentPlatform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// bundledSidecarSums holds the sums handed over via SetBundledSidecarSum,
// keyed by "<bin>/<GOOS>-<GOARCH>". Empty until the program's startup calls
// it — which is exactly the unmeasured state this package must tell apart
// from a genuinely measured sum.
var bundledSidecarSums = map[string]string{}

// SetBundledSidecarSum records the checksum a build measured for one bundled
// sidecar binary on the given platform ("<GOOS>-<GOARCH>", e.g.
// "linux-amd64"). The program's entry point calls it once per sidecar at
// startup; this package and the sidecars never do, because a sidecar links
// this package and a sum it could reach would become part of the very bytes
// that sum measures.
func SetBundledSidecarSum(bin, platform, sum string) {
	bundledSidecarSums[bin+"/"+platform] = sum
}

// bundledSidecarSum reads the checksum this build was released with for one
// bundled sidecar, for the platform this process is running on right now.
var bundledSidecarSum = func(bin string) string {
	return bundledSidecarSums[bin+"/"+currentPlatform()]
}
