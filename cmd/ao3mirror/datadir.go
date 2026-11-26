package main

import (
	"errors"
	"os"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/dirs"
)

// imageDir is the image worker's directory under data_dir, which `serve` reads images from.
const imageDir = "images"

// dataDirs is the data directory, opened once by its configured path (it must exist), and the
// image directory inside it. Every file a command makes, reads or removes under data_dir goes
// through one of these roots (or one opened inside them), so nothing leaves data_dir.
type dataDirs struct {
	root   *os.Root
	images *os.Root
}

// openDataDirs opens cfg.DataDir and creates the image directory in it when missing.
func openDataDirs(cfg config.Config) (*dataDirs, error) {
	root, err := dirs.OpenData(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	images, err := dirs.Sub(root, imageDir, 0o750)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return &dataDirs{root: root, images: images}, nil
}

// Close closes both roots.
func (d *dataDirs) Close() error { return errors.Join(d.images.Close(), d.root.Close()) }
