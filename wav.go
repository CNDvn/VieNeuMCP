package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

// patchWAV rewrites the streaming WAV header VieNeu emits.
// The server sets the RIFF and data sizes to 0xFFFFFFFF so a player can start
// before the utterance ends. A file saved after the body is complete needs the
// real sizes, or some players refuse it.
//
// dataBytes is the PCM payload size when the header is the 44-byte PCM header
// this server writes. It is 0 when the file is not that header; the bytes are
// left untouched.
func patchWAV(path string) (dataBytes int64, err error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() < 44 {
		return 0, nil
	}
	var hdr [44]byte
	if _, err := f.ReadAt(hdr[:], 0); err != nil {
		return 0, err
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" || string(hdr[36:40]) != "data" {
		return 0, nil
	}
	size := info.Size()
	if size > int64(^uint32(0)) {
		return 0, fmt.Errorf("file WAV lớn hơn 4 GiB")
	}
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(size-8))
	if _, err := f.WriteAt(buf[:], 4); err != nil {
		return 0, err
	}
	binary.LittleEndian.PutUint32(buf[:], uint32(size-44))
	if _, err := f.WriteAt(buf[:], 40); err != nil {
		return 0, err
	}
	return size - 44, nil
}
