//go:build !darwin

package main

import "fmt"

func main() { fmt.Println("clavis-imk: InputMethodKit is only available on macOS") }
