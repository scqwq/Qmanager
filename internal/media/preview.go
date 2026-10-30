package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
)

// Thumbnail never returns source bytes or accepts a caller-supplied disk path.
func Thumbnail(ctx context.Context, item Item) ([]byte, error) {
	if !item.Preview || item.Status != "recognized" {
		return nil, errors.New("该文件暂不支持预览")
	}
	f, err := OpenItem(item)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	original, ok := item.InfoIdentity.(os.FileInfo)
	if (ok && !os.SameFile(original, before)) || before.Size() != item.Bytes || !before.ModTime().Equal(readTime(item)) {
		return nil, errors.New("文件已变化，请重新扫描")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, _, err := image.DecodeConfig(io.NewSectionReader(f, 0, min(before.Size(), 1024*1024)))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 16_000_000 || before.Size() > 16*1024*1024 {
		return nil, errors.New("超过预览资源限制")
	}
	img, _, err := image.Decode(io.NewSectionReader(f, 0, before.Size()))
	if err != nil {
		return nil, errors.New("图片解码失败")
	}
	width, height := config.Width, config.Height
	if width > 480 || height > 480 {
		if width >= height {
			height = max(1, height*480/width)
			width = 480
		} else {
			width = max(1, width*480/height)
			height = 480
		}
	}
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	bounds := img.Bounds()
	for y := 0; y < height; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < width; x++ {
			r, g, b, a := img.At(bounds.Min.X+x*bounds.Dx()/width, bounds.Min.Y+y*bounds.Dy()/height).RGBA()
			out.SetRGBA(x, y, color.RGBA{uint8((r + 65535 - a) >> 8), uint8((g + 65535 - a) >> 8), uint8((b + 65535 - a) >> 8), 255})
		}
	}
	after, err := f.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("预览期间文件发生变化")
	}
	pathAfter, e := openRead(item.Root, item.Path)
	if e != nil {
		return nil, errors.New("源路径发生变化")
	}
	pathInfo, e := pathAfter.Stat()
	pathAfter.Close()
	if e != nil || !os.SameFile(before, pathInfo) {
		return nil, errors.New("源文件被替换")
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
