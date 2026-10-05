// 生成应用图标：蓝色渐变圆角方块 + 双向传输箭头（白，带投影）
// 4x 超采样抗锯齿渲染 256px 母版，再缩放出 16-128px，合成多尺寸 .ico
// 输出：installer/trans.ico、assets/icon.ico（托盘用）、installer/trans-256.png
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
)

const (
	S  = 256 // 目标尺寸
	SS = 4   // 超采样倍数
)

/* ---------- 形状判定（坐标基于 S 空间） ---------- */

func inRoundedRect(x, y float64) bool {
	const m = 10.0 // 边距
	const r = 58.0 // 圆角半径
	if x < m || y < m || x > S-m || y > S-m {
		return false
	}
	cx := clamp(x, m+r, S-m-r)
	cy := clamp(y, m+r, S-m-r)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

// 上箭头（左上）：头 y64..102（半宽 15→40），杆 y102..152（半宽 15）
func inUpArrow(x, y float64) bool {
	const cx = 105.0
	if y >= 64 && y <= 102 {
		t := (102 - y) / 38
		return abs(x-cx) <= 15+t*25
	}
	if y > 102 && y <= 152 {
		return abs(x-cx) <= 15
	}
	return false
}

// 下箭头（右下）：杆 y108..158（半宽 15），头 y158..196（半宽 15→40）
func inDownArrow(x, y float64) bool {
	const cx = 151.0
	if y >= 108 && y <= 158 {
		return abs(x-cx) <= 15
	}
	if y > 158 && y <= 196 {
		t := (y - 158) / 38
		return abs(x-cx) <= 15+t*25
	}
	return false
}

func abs(f float64) float64 { if f < 0 { return -f }; return f }
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

/* ---------- 渲染 ---------- */

type rgb struct{ r, g, b float64 }

var (
	gradTop = rgb{0x6f, 0xab, 0xff}
	gradBot = rgb{0x1c, 0x4f, 0xc7}
	shadowC = rgb{0x0a, 0x27, 0x6e}
)

func sample(x, y float64) (uint8, uint8, uint8, uint8) {
	if !inRoundedRect(x, y) {
		return 0, 0, 0, 0
	}
	t := y / S
	bg := rgb{lerp(gradTop.r, gradBot.r, t), lerp(gradTop.g, gradBot.g, t), lerp(gradTop.b, gradBot.b, t)}
	// 顶部轻微高光
	if hl := (96 - y) / 96 * 14; hl > 0 {
		bg = rgb{min255(bg.r + hl), min255(bg.g + hl), min255(bg.b + hl/2)}
	}
	// 箭头投影（右下偏移 3,4）
	if inUpArrow(x-3, y-4) || inDownArrow(x-3, y-4) {
		bg = rgb{lerp(bg.r, shadowC.r, 0.38), lerp(bg.g, shadowC.g, 0.38), lerp(bg.b, shadowC.b, 0.38)}
	}
	if inUpArrow(x, y) || inDownArrow(x, y) {
		return 252, 253, 255, 255
	}
	return uint8(bg.r), uint8(bg.g), uint8(bg.b), 255
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }
func min255(f float64) float64    { if f > 255 { return 255 }; return f }

func renderMaster() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, S, S))
	for py := 0; py < S; py++ {
		for px := 0; px < S; px++ {
			var rs, gs, bs, as, n uint32
			for sy := 0; sy < SS; sy++ {
				for sx := 0; sx < SS; sx++ {
					x := float64(px) + (float64(sx)+0.5)/SS
					y := float64(py) + (float64(sy)+0.5)/SS
					r, g, b, a := sample(x, y)
					rs += uint32(r)
					gs += uint32(g)
					bs += uint32(b)
					as += uint32(a)
					n++
				}
			}
			i := img.PixOffset(px, py)
			img.Pix[i] = uint8(rs / n)
			img.Pix[i+1] = uint8(gs / n)
			img.Pix[i+2] = uint8(bs / n)
			img.Pix[i+3] = uint8(as / n)
		}
	}
	return img
}

/* ---------- 缩放（盒平均） ---------- */

func scaleTo(img image.Image, w int) image.Image {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	h := sh * w / sw
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var rs, gs, bs, as, n uint32
			sx0, sx1 := x*sw/w, (x+1)*sw/w
			sy0, sy1 := y*sh/h, (y+1)*sh/h
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			if sy1 <= sy0 {
				sy1 = sy0 + 1
			}
			for sy := sy0; sy < sy1 && sy < sh; sy++ {
				for sx := sx0; sx < sx1 && sx < sw; sx++ {
					r, g, bl, a := img.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					rs += r >> 8
					gs += g >> 8
					bs += bl >> 8
					as += a >> 8
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i] = uint8(rs / n)
			dst.Pix[i+1] = uint8(gs / n)
			dst.Pix[i+2] = uint8(bs / n)
			dst.Pix[i+3] = uint8(as / n)
		}
	}
	return dst
}

/* ---------- ICO 合成（≥128 用 PNG 条目，小尺寸用 DIB 保证兼容） ---------- */

func dibBytes(im image.Image) []byte {
	b := im.Bounds()
	w, h := b.Dx(), b.Dy()
	var out bytes.Buffer
	hdr := make([]byte, 40)
	binary.LittleEndian.PutUint32(hdr[0:], 40)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(w))
	binary.LittleEndian.PutUint32(hdr[8:], uint32(h*2))
	binary.LittleEndian.PutUint16(hdr[12:], 1)
	binary.LittleEndian.PutUint16(hdr[14:], 32)
	out.Write(hdr)
	for y := h - 1; y >= 0; y-- { // DIB 自下而上
		for x := 0; x < w; x++ {
			r, g, bl, a := im.At(b.Min.X+x, b.Min.Y+y).RGBA()
			out.Write([]byte{byte(bl >> 8), byte(g >> 8), byte(r >> 8), byte(a >> 8)}) // BGRA
		}
	}
	out.Write(make([]byte, ((w+31)/32)*4*h)) // AND 掩码全 0
	return out.Bytes()
}

func writeICO(path string, master image.Image) {
	type entry struct {
		w    int
		data []byte
	}
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	entries := []entry{}
	for _, sz := range sizes {
		im := image.Image(master)
		if sz != S {
			im = scaleTo(master, sz)
		}
		if sz >= 128 {
			var b bytes.Buffer
			if err := png.Encode(&b, im); err != nil {
				panic(err)
			}
			entries = append(entries, entry{sz, b.Bytes()})
		} else {
			entries = append(entries, entry{sz, dibBytes(im)})
		}
	}
	var ico bytes.Buffer
	ico.Write([]byte{0, 0, 1, 0, byte(len(entries)), 0})
	offset := 6 + 16*len(entries)
	for _, e := range entries {
		w := byte(e.w)
		if e.w == 256 {
			w = 0
		}
		ico.WriteByte(w)
		ico.WriteByte(w)
		ico.Write([]byte{0, 0, 1, 0, 32, 0})
		binary.Write(&ico, binary.LittleEndian, uint32(len(e.data)))
		binary.Write(&ico, binary.LittleEndian, uint32(offset))
		offset += len(e.data)
	}
	for _, e := range entries {
		ico.Write(e.data)
	}
	if err := os.WriteFile(path, ico.Bytes(), 0o644); err != nil {
		panic(err)
	}
}

func main() {
	master := renderMaster()
	_ = os.MkdirAll("installer", 0o755)
	_ = os.MkdirAll("assets", 0o755)

	writeICO(filepath.Join("installer", "trans.ico"), master)
	writeICO(filepath.Join("assets", "icon.ico"), master)

	f, err := os.Create(filepath.Join("installer", "trans-256.png"))
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, master); err != nil {
		panic(err)
	}
	fmt.Println("已生成 installer/trans.ico、assets/icon.ico、installer/trans-256.png")
}
