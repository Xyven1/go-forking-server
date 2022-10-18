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
	v  T          // value of type T
	mu sync.Mutex // mutex to protect v
}

type ForkingServer struct {
	serial WrapSync[serial.Port]  // the serial port we are listnening on
	udp    WrapSync[*net.UDPConn] // the udp port we are using to broadcast
	conns  map[net.Conn]bool      // a map of all current tcp connections
	addrs  map[string]int         // a list of the addresses of all current tcp connections
	port   string                 // name of the serial port to connect to
}

func main() {
	args := os.Args[1:]

	if len(args) != 2 {
		log.Fatal(`Usage: ./main <file> <port>
<file> - For windows use "COMx" for linux use "/dev/ttyx". Globs are supported (you must surround the argument in quotes), but first file will always be used.
<port> - The port to listen on`)
	}

	server := ForkingServer{}
	server.port = args[0]
	server.serial.mu.Lock()
	server.startSerial()
	server.serial.mu.Unlock()

	l, err := net.Listen("tcp", ":"+args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()

	server.udp.v, err = net.ListenUDP("udp", &net.UDPAddr{IP: nil, Port: 14550})
	if err != nil {
		log.Fatal(err)
	}

	go server.readFileAndSendToAll()
	go server.udpReceive()
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Fatal(err)
			continue
		}
		go server.handleConnection(conn)
	}
}

// call with s.mu locked
func (server *ForkingServer) startSerial() {
	osName := runtime.GOOS
	mode := &serial.Mode{}
	var sn string
	switch osName {
	case "linux":
		fs, err := filepath.Glob(server.port)
		if err != nil {
			log.Fatal(err)
		}
		if len(fs) == 0 {
			log.Fatal("No files found")
		}
		sn = fs[0]
	case "windows":
		if server.port[:3] != "COM" {
			log.Fatal("Windows port must start with COM")
		}
		sn = server.port
	default:
		log.Fatal("Unsupported OS")
	}
	var err error
	server.serial.v, err = serial.Open(sn, mode)
	if err != nil {
		log.Fatal(err)
	}
}

func (server *ForkingServer) udpReceive() {
	for {
		buf := make([]byte, 1024)
		n, addr, err := server.udp.v.ReadFromUDP(buf)
		if err != nil {
			fmt.Println(err)
			continue
		}
		if server.addrs[addr.IP.String()] > 0 && n > 0 {
			server.serial.mu.Lock()
			server.serial.v.Write(buf[:n])
			server.serial.mu.Unlock()
		}
	}
}

func (server *ForkingServer) handleConnection(c net.Conn) {
	defer c.Close()
	a, err := net.ResolveTCPAddr("tcp", c.RemoteAddr().String())
	if err != nil {
		fmt.Println(err)
		return
	}
	server.addrs[a.IP.String()]++
	server.conns[c] = true
	b := make([]byte, 1024)
	for {
		n, err := c.Read(b)
		if err != nil {
			break
		}
		server.serial.mu.Lock()
		server.serial.v.Write(b[:n])
		server.serial.mu.Unlock()
	}
	server.addrs[a.IP.String()]--
	delete(server.conns, c)
}

func udpBroadcast(u *WrapSync[*net.UDPConn], r net.Addr, b []byte) {
	a, err := net.ResolveUDPAddr("udp", r.String())
	if err != nil {
		fmt.Println(err)
		return
	}
	a.Port = 14550
	u.mu.Lock()
	_, err = u.v.WriteToUDP(b, a)
	u.mu.Unlock()
	if err != nil {
		fmt.Println(err)
	}
}

func (server *ForkingServer) readFileAndSendToAll() {
	fmt.Println("Loading file")
	b := make([]byte, 1024)
	c := 0
	t := time.Now()
	o, err := os.Stdout.Stat()
	if err != nil {
		log.Fatal(err)
	}

	for {
		server.serial.mu.Lock()
		n, err := server.serial.v.Read(b)
		if err != nil {
			fmt.Println(err)
			server.serial.v.Close()
			server.startSerial()
		}
		server.serial.mu.Unlock()

		if (o.Mode() & os.ModeCharDevice) == os.ModeCharDevice {
			fmt.Printf("\r%s\tNum Clients: %d\tNum Mavlink Packets: %d  ", time.Now().Format("2006/01/02 15:04:05"), len(server.conns), c)
		} else {
			if time.Now().Sub(t) > time.Second*10 {
				fmt.Printf("%s\tNum Clients: %d\tNum Mavlink Packets: %d\n", time.Now().Format("2006/01/02 15:04:05"), len(server.conns), c)
				t = time.Now()
			}
		}
		c++
		for conn := range server.conns {
			conn.Write(b[:n])
			udpBroadcast(&server.udp, conn.RemoteAddr(), b[:n])
		}
	}
}
