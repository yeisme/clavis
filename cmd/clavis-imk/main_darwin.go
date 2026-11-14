//go:build darwin

package main

import (
	"flag"
	"fmt"
	"github.com/ebitengine/purego"
	"os"
)

func main() {
	smoke := flag.Bool("smoke-check", false, "validate InputMethodKit framework availability")
	flag.Parse()
	if *smoke {
		if _, e := purego.Dlopen("/System/Library/Frameworks/InputMethodKit.framework/InputMethodKit", purego.RTLD_LAZY); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		fmt.Println("clavis-imk: smoke-check ok")
		return
	}
	fmt.Println("clavis-imk: helper requires InputMethodKit host")
}
