package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/exp/slices"

	"github.com/fanaticscripter/wechat-sticker-exporter/sticker"
	log "github.com/sirupsen/logrus"
)

var (
	_rootdir string
	_datadir string
)

func init() {
	log.SetFormatter(&log.TextFormatter{})
	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("failed to determine executable path: %s", err)
	}
	if strings.HasPrefix(exe, os.TempDir()) {
		// Probably `go run`.
		_rootdir = "."
	} else {
		_rootdir = filepath.Dir(exe)
	}
	_datadir = filepath.Join(_rootdir, "data")
	if err := os.MkdirAll(_datadir, 0o755); err != nil {
		log.Fatalf("failed to create directory %q: %s", _datadir, err)
	}
}

func main() {
	dirents, err := os.ReadDir(_datadir)
	if err != nil {
		log.Fatalf("failed to read data dir %q for existing entries: %s", _datadir, err)
	}
	existingIDs := make(map[string]struct{})
	filenamePattern := regexp.MustCompile(`^([0-9a-f]+)\.[0-9a-z]+$`)
	for _, e := range dirents {
		filename := e.Name()
		m := filenamePattern.FindStringSubmatch(filename)
		if m == nil {
			continue
		}
		existingIDs[m[1]] = struct{}{}
	}

	var errored bool
	stickers, err := sticker.ExtractStickerMetadata()
	if err != nil {
		log.Error(err)
		errored = true
	}
	slices.SortFunc(stickers, func(s1, s2 sticker.Sticker) int {
		return s1.Mtime.Compare(s2.Mtime)
	})
	var newDownloads []sticker.Sticker
	var failures int
	for _, s := range stickers {
		if _, exists := existingIDs[s.ID]; exists {
			log.Infof("sticker %s already downloaded", s.ID)
			continue
		}
		err = s.DownloadTo(_datadir)
		if err != nil {
			log.Error(err)
			failures++
		} else {
			newDownloads = append(newDownloads, s)
		}
	}
	if len(newDownloads) > 0 {
		fmt.Printf("downloaded %d new stickers:\n", len(newDownloads))
		for _, s := range newDownloads {
			fmt.Printf("%s\n", s.DownloadedPath)
		}
	} else {
		fmt.Println("no new stickers downloaded")
	}
	if failures > 0 {
		log.Errorf("failed to download %d sticker(s)", failures)
		errored = true
	}

	if errored {
		os.Exit(1)
	}
}
