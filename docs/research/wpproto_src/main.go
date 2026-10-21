package main

import (
	"log"

	"github.com/egoist/mygo"
)

func main() {
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{Width: 800, Height: 600, Frameless: true, SkipTaskbar: true})
		win.Page().LoadHTML(`<body style="margin:0;background:#123"></body>`, "")
		attachDesktop(win.NativeHandle())
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
