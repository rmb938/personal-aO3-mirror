package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/rmb938/personal-aO3-mirror/internal/imagefetch"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Reads of downloaded images for the API's image route. An image's bytes are fanwork content:
// never log them. Its hash, content type and size are not.

// MaxImageFileBytes caps the image file GetImage reads. The image worker stores at most
// images.max_bytes (25 MiB by default), and the API buffers a response up to 32 MiB.
const MaxImageFileBytes = 32 << 20

// Image is a downloaded image: the Content-Type its host served it with (image/* only) and its
// bytes.
type Image struct {
	ContentType string
	Body        []byte
}

// errImageFile means a downloaded image's file is unreadable, too large or does not match its
// hash. It is an internal error (the store and the image directory disagree), logged with the
// image id.
var errImageFile = errors.New("controllers: image file does not match its row")

// GetImage returns the downloaded image with hash sha (64 lowercase hex characters), or
// store.ErrNoImage: for a malformed hash, an image never downloaded, or one whose file is gone.
// The file is read after the transaction ends and checked against its hash.
func (c *Controllers) GetImage(ctx context.Context, sha string) (Image, error) {
	if !ValidSHA256(sha) {
		return Image{}, store.ErrNoImage // also keeps imagefetch.Path to a well-formed name
	}
	img, err := store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (store.StoredImage, error) {
		return tx.ImageBySHA(ctx, sha)
	})
	if err != nil {
		return Image{}, err
	}
	log := c.logger(ctx).With("image_id", img.ID, "content_type", img.ContentType, "bytes", img.Bytes)
	if c.options.ImageDir == nil {
		return Image{}, errors.New("controllers: no image directory")
	}
	body, err := readImageFile(c.options.ImageDir, imagefetch.File(img.SHA256, img.ContentType))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		log.Warn("image file missing; reported as no image")
		return Image{}, store.ErrNoImage
	case err != nil:
		log.Error("image file unreadable", "err", err.Error())
		return Image{}, fmt.Errorf("%w (image %d)", errImageFile, img.ID)
	}
	if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != img.SHA256 {
		log.Error("image file does not match its hash", "file_bytes", len(body))
		return Image{}, fmt.Errorf("%w (image %d)", errImageFile, img.ID)
	}
	return Image{ContentType: img.ContentType, Body: body}, nil
}

// readImageFile reads at most MaxImageFileBytes from file name in dir; a larger file is an
// error. The error holds the name, which is made of the hash and the extension only.
func readImageFile(dir *os.Root, name string) (body []byte, err error) {
	f, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	body, err = io.ReadAll(io.LimitReader(f, MaxImageFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxImageFileBytes {
		return nil, fmt.Errorf("image file over %d bytes", MaxImageFileBytes)
	}
	return body, nil
}

// ValidSHA256 reports whether s is a hex SHA-256 as imagefetch writes it: 64 lowercase hex
// characters.
func ValidSHA256(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	for i := range len(s) {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
