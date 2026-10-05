// 生成 resource.syso（exe 图标 + 版本信息），go build 会自动打包进 trans.exe
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/josephspurrier/goversioninfo"
)

func main() {
	icon := flag.String("icon", "installer/trans.ico", "图标路径")
	spec := flag.String("spec", "versioninfo.json", "版本描述 JSON")
	out := flag.String("o", "resource.syso", "输出 .syso")
	flag.Parse()

	b, err := os.ReadFile(*spec)
	if err != nil {
		panic(err)
	}
	vi := &goversioninfo.VersionInfo{}
	if err := vi.ParseJSON(b); err != nil {
		panic(err)
	}
	vi.IconPath = *icon
	vi.Build()
	vi.Walk() // 序列化进 Buffer（漏了这步 syso 里就只有图标没有版本信息）
	if err := vi.WriteSyso(*out, runtime.GOARCH); err != nil {
		panic(err)
	}
	fmt.Println("已生成", *out)
}
