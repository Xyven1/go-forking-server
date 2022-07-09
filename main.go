package main

import (
	"fmt"
	"log"
	"net"
	"os"
)

func main() {
	// Hello world, the web server

	args := os.Args[1:]
	if len(args) != 2 {
		log.Fatal("Usage: ./main <file> <port>")
	}

	f, err := os.Open(args[0])
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	defer f.Close()

	l, err := net.Listen("tcp", ":"+args[1])
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	defer l.Close()

	// create list of conns
	conns := make(map[net.Conn]bool)
	go ReadFileAndSendToAll(f, conns)
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Fatal(err)
			continue
		}
		conns[conn] = true
		go HandleConnection(conn, f, conns)
	}
}

func HandleConnection(c net.Conn, f *os.File, conns map[net.Conn]bool) {
	defer c.Close()
  fmt.Println("Handling connection")
  b := make([]byte, 1024)
	for s, err := c.Read(b); s > 0 && err == nil; {
    f.Write(b[:s])
	}
  delete(conns, c)
}

func ReadFileAndSendToAll(f *os.File, conns map[net.Conn]bool) {
  fmt.Println("Reading file")
	b := make([]byte, 1024)
	for s, err := f.Read(b); s > 0 && err == nil; {
    fmt.Println("Sending to all: ", s)
		for conn := range conns {
			if conns[conn] {
				conn.Write(b[:s])
			}
		}
	}
}
