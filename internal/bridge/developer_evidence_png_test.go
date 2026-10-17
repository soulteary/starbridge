package bridge

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

func TestEvidenceRejectsHeaderOnlyOrCorruptedPNGBeforeWriting(t *testing.T) {
	bitmap := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	bitmap.SetNRGBA(0, 0, color.NRGBA{R: 20, G: 80, B: 140, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, bitmap); err != nil {
		t.Fatal(err)
	}
	validPNG := encoded.Bytes()
	badCRC := append([]byte{}, validPNG...)
	idat := bytes.Index(badCRC, []byte("IDAT"))
	if idat < 0 {
		t.Fatal("synthetic PNG has no pixel chunk")
	}
	badCRC[idat+4] ^= 1
	for _, test := range []struct {
		name    string
		content []byte
	}{
		{"header without pixels", validPNG[:33]},
		{"truncated pixel data", validPNG[:len(validPNG)-13]},
		{"corrupted pixel CRC", badCRC},
		{"missing IEND", validPNG[:len(validPNG)-12]},
	} {
		t.Run(test.name, func(t *testing.T) {
			// This is precisely the former gap: header inspection succeeds even
			// though the entire image cannot be decoded.
			if _, err := png.DecodeConfig(bytes.NewReader(test.content)); err != nil {
				t.Fatal("fixture does not reproduce header-only validation")
			}
			directory := t.TempDir()
			result := Success(map[string]any{"png_base64": base64.StdEncoding.EncodeToString(test.content), "width": 1, "height": 1})
			_, err := evidencePayload(directory, "synthetic", "screenshot.capture", result)
			checkCode(t, err, "INVALID_GUEST_RESPONSE")
			files, err := os.ReadDir(directory)
			if err != nil || len(files) != 0 {
				t.Fatal("invalid image left a PNG or JSON artifact that could be mistaken for captured evidence")
			}
		})
	}
}
