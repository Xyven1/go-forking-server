package main

import (
	"flag"
	"fmt"
	"os"
)

var (
	port    = flag.Uint("port", 5050, "The port to listen on")
	webport = flag.Uint("webport", 8080, "The port to listen on for the web server")
)
var Usage = func() {
	fmt.Println(`Usage: ./main <file> [options]`)
	flag.PrintDefaults()
}

func main() {
	flag.Usage = Usage
	flag.Parse()
	if flag.NArg() < 1 {
		Usage()
		os.Exit(1)
	}
	args := flag.Args()

	server := NewForkingServer(args[0], *port, *webport)
	go server.startWebServer()
	go server.logging()
	go server.readFileAndSendToAll()
	go server.udpReceive()
	server.listen()
}
