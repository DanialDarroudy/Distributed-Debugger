package main

import (
	"os"
	"path/filepath"

	"github.com/mihkeltiks/rev-mpi-deb/logger"
)

func precleanup() {
	// remove  artefacts from previous run
	removeTempFiles()
}

func removeTempFiles() {
	logger.Debug("removing temporary files..")

	dir, err := os.ReadDir("bin/temp")
	if err != nil {
		return
	}

	for _, d := range dir {
		if d.Name() != ".gitkeep" {
			_ = os.RemoveAll(filepath.Join("bin/temp", d.Name()))
		}
	}
}
