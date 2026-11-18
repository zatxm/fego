//go:build race

package platform

// The race detector makes sync.Pool drop items at random, so purego's pooled
// call frames can allocate; allocation checks with many calls account for it.
func init() { raceEnabled = true }
