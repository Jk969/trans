// 生成 resource.syso（exe 图标 + 版本信息），go build 会自动打包进 exe
// 版本号单一来源：根目录 VERSION 文件（注入 FixedFileInfo/StringFileInfo，覆盖 JSON 里的值）
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/josephspurrier/goversioninfo"
)

func main() {
	icon := flag.String("icon", "installer/trans.ico", "图标路径")
	spec := flag.String("spec", "versioninfo.json", "版本描述 JSON")
	verFile := flag.String("version-file", "VERSION", "版本号单一来源文件")
	out := flag.String("o", "resource.syso", "输出 .syso")
	flag.Parse()

	ver := strings.TrimSpace(readOrPanic(*verFile))
	parts := strings.Split(ver, ".")
	num := func(i int) int {
		if i >= len(parts) {
			return 0
		}
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			return 0
		}
		return n
	}
	fv := goversioninfo.FileVersion{Major: num(0), Minor: num(1), Patch: num(2), Build: 0}

	b, err := os.ReadFile(*spec)
	if err != nil {
		panic(err)
	}
	vi := &goversioninfo.VersionInfo{}
	if err := vi.ParseJSON(b); err != nil {
		panic(err)
	}
	// 注入版本（versioninfo.json 只保留描述类字段，不再写版本号）
	vi.FixedFileInfo.FileVersion = fv
	vi.FixedFileInfo.ProductVersion = fv
	vi.StringFileInfo.FileVersion = fmt.Sprintf("%d.%d.%d.0", fv.Major, fv.Minor, fv.Patch)
	vi.StringFileInfo.ProductVersion = ver

	vi.IconPath = *icon
	vi.Build()
	vi.Walk() // 序列化进 Buffer（漏了这步 syso 里就只有图标没有版本信息）
	if err := vi.WriteSyso(*out, runtime.GOARCH); err != nil {
		panic(err)
	}
	fmt.Printf("已生成 %s（版本 %s）\n", *out, ver)
}

func readOrPanic(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		panic("读版本文件失败 " + p + ": " + err.Error())
	}
	return string(b)
}
