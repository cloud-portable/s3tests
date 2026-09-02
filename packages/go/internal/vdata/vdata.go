// Package vdata caches a vector's materialized datasets so multi-megabyte
// $prng streams are generated once per vector, however many times their bytes
// or derived values (${data.<name>.<field>}) are referenced.
package vdata

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"hash/crc64"
	"strconv"

	s3vectors "github.com/cloud-portable/s3vectors/packages/go"
	"github.com/cloud-portable/s3vectors/packages/go/datagen"
)

var (
	crc32cTable = crc32.MakeTable(crc32.Castagnoli)
	crc64Table  = crc64.MakeTable(0x9A6C9329AC4BC9B5) // CRC-64/NVME, as in datagen
)

// Cache materializes and memoizes one vector's datasets. Not safe for
// concurrent use; each vector executes on a single goroutine.
type Cache struct {
	specs map[string]s3vectors.DataSpec
	bytes map[string][]byte
}

func New(specs map[string]s3vectors.DataSpec) *Cache {
	return &Cache{specs: specs, bytes: map[string][]byte{}}
}

// Bytes returns the dataset's bytes (generated on first use). The returned
// slice is shared — callers must not mutate it.
func (c *Cache) Bytes(name string) ([]byte, error) {
	if b, ok := c.bytes[name]; ok {
		return b, nil
	}
	b, err := datagen.Generate(c.specs, name)
	if err != nil {
		return nil, err
	}
	c.bytes[name] = b
	return b, nil
}

// Derived computes a ${data.<name>.<field>} value from the cached bytes.
// Field semantics mirror datagen.Derived exactly.
func (c *Cache) Derived(name, field string) (string, error) {
	b, err := c.Bytes(name)
	if err != nil {
		return "", err
	}
	switch field {
	case "size":
		return strconv.Itoa(len(b)), nil
	case "md5":
		sum := md5.Sum(b)
		return hex.EncodeToString(sum[:]), nil
	case "etag":
		sum := md5.Sum(b)
		return `"` + hex.EncodeToString(sum[:]) + `"`, nil
	case "sha256":
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:]), nil
	case "sha256B64":
		sum := sha256.Sum256(b)
		return base64.StdEncoding.EncodeToString(sum[:]), nil
	case "sha1B64":
		sum := sha1.Sum(b)
		return base64.StdEncoding.EncodeToString(sum[:]), nil
	case "crc32B64":
		var v [4]byte
		binary.BigEndian.PutUint32(v[:], crc32.ChecksumIEEE(b))
		return base64.StdEncoding.EncodeToString(v[:]), nil
	case "crc32cB64":
		var v [4]byte
		binary.BigEndian.PutUint32(v[:], crc32.Checksum(b, crc32cTable))
		return base64.StdEncoding.EncodeToString(v[:]), nil
	case "crc64nvmeB64":
		var v [8]byte
		binary.BigEndian.PutUint64(v[:], crc64.Checksum(b, crc64Table))
		return base64.StdEncoding.EncodeToString(v[:]), nil
	default:
		return "", fmt.Errorf("unknown derived data field: %s", field)
	}
}
