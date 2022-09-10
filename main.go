package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"go.bug.st/serial"
)

type WrapSync[T any] struct {
	v  T
	mu sync.Mutex
}

func main() {
	args := os.Args[1:]
	osName := runtime.GOOS

	if len(args) != 2 {
		log.Fatal(`Usage: ./main <file> <port>
<file> - For windows use "COMx" for linux use "/dev/ttyx". Globs are supported (you must surround the argument in quotes), but first file will always be used.
<port> - The port to listen on`)
	}

	var sn string
	if osName == "linux" {
		fs, err := filepath.Glob(args[0])
		if err != nil {
			log.Fatal(err)
		}
		if len(fs) == 0 {
			log.Fatal("No files found")
		}
		sn = fs[0]
	} else if osName == "windows" {
		if args[0][:3] != "COM" {
			log.Fatal("Windows port must start with COM")
		}
		sn = args[0]
	} else {
		log.Fatal("Unsupported OS")
	}

	mode := &serial.Mode{}
	s, err := serial.Open(sn, mode)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	serial := WrapSync[serial.Port]{v: s}
	defer s.Close()

	l, err := net.Listen("tcp", ":"+args[1])
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	defer l.Close()

	udp := WrapSync[net.UDPConn]{}
	conns := make(map[net.Conn]bool)
	addrs := make(map[net.Addr]int)
	go readFileAndSendToAll(&serial, &udp, conns)
	go udpReceive(&udp, &serial, addrs)
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Fatal(err)
			continue
		}
		go handleConnection(conn, &udp, &serial, conns, addrs)
	}
}

func udpReceive(u *WrapSync[net.UDPConn], s *WrapSync[serial.Port], addrs map[net.Addr]int) {
	for {
		buf := make([]byte, 1024)
		n, addr, err := u.v.ReadFromUDP(buf)
		if err != nil {
			break
		}
		if addrs[addr] > 0 && n > 0 {
			s.mu.Lock()
			s.v.Write(buf[:n])
			s.mu.Unlock()
		}
	}
}

func handleConnection(c net.Conn, u *WrapSync[net.UDPConn], s *WrapSync[serial.Port], conns map[net.Conn]bool, addrs map[net.Addr]int) {
	defer c.Close()
	addrs[c.LocalAddr()]++
	conns[c] = true
	b := make([]byte, 1024)
	for {
		n, err := c.Read(b)
		if err != nil {
			break
		}
		s.mu.Lock()
		s.v.Write(b[:n])
		s.mu.Unlock()
	}
	addrs[c.LocalAddr()]--
	delete(conns, c)
}

func udpBroadcast(u *WrapSync[net.UDPConn], addr net.Addr, b []byte) {
	a, err := net.ResolveUDPAddr("udp", addr.String())
	if err != nil {
		log.Fatal(err)
	}
	a.Port = 14550
	u.mu.Lock()
	u.v.WriteToUDP(b, a)
	u.mu.Unlock()
}

func readFileAndSendToAll(s *WrapSync[serial.Port], u *WrapSync[net.UDPConn], conns map[net.Conn]bool) {
	fmt.Println("Loading file")
	b := make([]byte, 1024)
	c := 0
	for {
		n, err := s.v.Read(b)
		if err != nil {
			continue
		}
		fmt.Printf("\r%s\tNum Clients: %d\tNum Mavlink Packets: %d", time.Now().Format("2006-01-02 15:04:05"), len(conns), c)
		c++
		for conn := range conns {
			conn.Write(b[:n])
			udpBroadcast(u, conn.LocalAddr(), b[:n])
		}
	}
}
