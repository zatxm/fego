//go:build benchprofile

package main

import (
	"log"
	"os"
	"runtime"
	"runtime/pprof"
)

func startProfile(rec *recorder) {
	path := os.Getenv("BENCH_CPU_PROFILE")
	if path == "" {
		return
	}
	file, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		log.Fatal(err)
	}
	rec.stopProfile = func() {
		pprof.StopCPUProfile()
		file.Close()
		if path := os.Getenv("BENCH_HEAP_PROFILE"); path != "" {
			f, err := os.Create(path)
			if err != nil {
				log.Fatal(err)
			}
			runtime.GC()
			if err := pprof.WriteHeapProfile(f); err != nil {
				log.Fatal(err)
			}
			f.Close()
		}
	}
}
