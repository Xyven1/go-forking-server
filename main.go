package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

func main() {
	args := os.Args[1:]
	if len(args) != 2 {
		log.Fatal("Usage: ./main <file> <port>")
	}

	f, err := os.OpenFile(args[0], os.O_RDWR, 0666)
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
	b := make([]byte, 1024)
	for {
		n, err := c.Read(b)
		if err != nil {
			break
		}
		f.Write(b[:n])
	}
	delete(conns, c)
}

func ReadFileAndSendToAll(f *os.File, conns map[net.Conn]bool) {
	fmt.Println("Loading file")
	b := make([]byte, 1024)
	c := 0
	for {
		n, err := f.Read(b)
		if err != nil {
			continue
		}
		fmt.Printf("\r%s\tNum Clients: %d\tNum Mavlink Packets: %d", time.Now().Format("2006-01-02 15:04:05"), len(conns), c)
		c++
		for conn := range conns {
			conn.Write(b[:n])
		}
	}
}
