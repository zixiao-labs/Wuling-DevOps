package authhttp

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFile adapts a bytes.Reader into the multipart.File interface that
// decodeAvatar accepts. multipart.File is io.Reader + io.ReaderAt + io.Seeker
// + io.Closer.
type fakeFile struct{ *bytes.Reader }

func (f *fakeFile) Close() error { return nil }

func TestDecodeAvatar_AcceptsPNG(t *testing.T) {
	src := makeSolidImage(128, 64, color.RGBA{R: 0x55, G: 0xaa, B: 0xff, A: 0xff})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, src))

	img, format, err := decodeAvatar(&fakeFile{bytes.NewReader(buf.Bytes())})
	require.Nil(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, 128, img.Bounds().Dx())
	assert.Equal(t, 64, img.Bounds().Dy())
}

func TestDecodeAvatar_AcceptsJPEG(t *testing.T) {
	src := makeSolidImage(200, 200, color.RGBA{R: 0xff, G: 0x00, B: 0x77, A: 0xff})
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, src, nil))

	_, format, err := decodeAvatar(&fakeFile{bytes.NewReader(buf.Bytes())})
	require.Nil(t, err)
	assert.Equal(t, "jpeg", format)
}

func TestDecodeAvatar_RejectsNonImage(t *testing.T) {
	_, _, err := decodeAvatar(&fakeFile{bytes.NewReader([]byte("not-an-image\nbut-pretends"))})
	require.NotNil(t, err)
	assert.Contains(t, strings.ToLower(err.Message), "decode")
}

func TestDecodeAvatar_RejectsTooSmall(t *testing.T) {
	src := makeSolidImage(4, 4, color.RGBA{A: 0xff})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, src))

	_, _, err := decodeAvatar(&fakeFile{bytes.NewReader(buf.Bytes())})
	require.NotNil(t, err)
	assert.Contains(t, strings.ToLower(err.Message), "dimensions")
}

func TestResize256_Square(t *testing.T) {
	src := makeSolidImage(1024, 1024, color.RGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xff})
	out := resize256(src)
	assert.Equal(t, AvatarSize, out.Bounds().Dx())
	assert.Equal(t, AvatarSize, out.Bounds().Dy())
}

func TestResize256_NonSquareCentreCrops(t *testing.T) {
	// 400 wide x 100 tall solid colour — after centre-crop and downscale we
	// expect a 256x256 image of the same colour.
	colr := color.RGBA{R: 0x44, G: 0x88, B: 0xcc, A: 0xff}
	src := makeSolidImage(400, 100, colr)
	out := resize256(src)
	assert.Equal(t, AvatarSize, out.Bounds().Dx())
	assert.Equal(t, AvatarSize, out.Bounds().Dy())

	// Sample the centre pixel — it should be (close to) the source colour.
	got := out.RGBAAt(AvatarSize/2, AvatarSize/2)
	assert.InDelta(t, float64(colr.R), float64(got.R), 4)
	assert.InDelta(t, float64(colr.G), float64(got.G), 4)
	assert.InDelta(t, float64(colr.B), float64(got.B), 4)
}

func makeSolidImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}
