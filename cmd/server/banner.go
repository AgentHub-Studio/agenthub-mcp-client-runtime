package main

import (
	_ "embed"
	"fmt"
)

//go:embed banner.txt
var bannerText string

func printBanner() {
	fmt.Print(bannerText)
}
