package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"go.bug.st/serial"
)

type WrapSync[T any] struct {
	v  T          // value of type T
	mu sync.Mutex // mutex to protect v
}

type ForkingServer struct {
	serial      WrapSync[serial.Port]       // the serial port we are listnening on
	udp         WrapSync[*net.UDPConn]      // the udp port we are using to broadcast
	conns       WrapSync[map[net.Conn]bool] // a map of all current tcp connections
	addrs       WrapSync[map[string]int]    // a list of the addresses of all current tcp connections
	device      string                      // the device we are listening on
	port        uint                        // the port we are listening on
	webport     uint                        // the port we are listening on for the web server
	num_packets atomic.Uint32               // number of packets sent
	num_clients atomic.Uint32               // number of clients connected
}

func NewForkingServer(device string, port uint, webport uint) *ForkingServer {
	server := &ForkingServer{
		device:  device,
		port:    port,
		webport: webport,
	}
	server.serial.v = nil
	server.udp.v = nil
	server.conns.v = make(map[net.Conn]bool)
	server.addrs.v = make(map[string]int)
	return server
}

// call with s.mu locked
func (server *ForkingServer) startSerial() {
	osName := runtime.GOOS
	mode := &serial.Mode{}
	var sn string
	for {
		err := func() error {
			switch osName {
			case "linux":
				fs, err := filepath.Glob(server.device)
				if err != nil {
					return err
				}
				if len(fs) == 0 {
					return fmt.Errorf("No devices found matching \"%s\"", server.device)
				}
				sn = fs[0]
			case "windows":
				if server.device[:3] != "COM" {
					log.Fatal("Windows port must start with COM")
				}
				sn = server.device
			default:
				log.Fatal("Unsupported OS")
			}
			var err error
			server.serial.v, err = serial.Open(sn, mode)
			return err
		}()
		if err != nil {
			log.Printf("Error opening serial port: %s\n", err)
			time.Sleep(time.Second)
		} else {
			break
		}
	}
}

func (server *ForkingServer) logging() {
	t := time.Now()
	o, err := os.Stdout.Stat()
	if err != nil {
		log.Fatal(err)
	}
	for {
		server.serial.mu.Lock()
		server.serial.mu.Unlock()
		if (o.Mode() & os.ModeCharDevice) == os.ModeCharDevice {
			if time.Now().Sub(t) > time.Millisecond*100 {
				fmt.Printf(
					"\r%s Num Clients: %d\tNum Mavlink Packets: %d  ",
					time.Now().Format("2006/01/02 15:04:05"),
					server.num_clients.Load(),
					server.num_packets.Load(),
				)
				t = time.Now()
			}
		} else {
			if time.Now().Sub(t) > time.Second*10 {
				log.Printf(
					"Num Clients: %d\tNum Mavlink Packets: %d",
					server.num_clients.Load(),
					server.num_packets.Load(),
				)
				t = time.Now()
			}
		}
	}
}

func (server *ForkingServer) udpReceive() {
	var err error
	server.udp.v, err = net.ListenUDP("udp", &net.UDPAddr{IP: nil, Port: 14550})
	if err != nil {
		log.Fatal(err)
	}
	for {
		buf := make([]byte, 1024)
		n, addr, err := server.udp.v.ReadFromUDP(buf)
		if err != nil {
			fmt.Println(err)
			continue
		}
		server.addrs.mu.Lock()
		present := server.addrs.v[addr.IP.String()] > 0
		server.addrs.mu.Unlock()
		if present && n > 0 {
			server.serial.mu.Lock()
			server.serial.v.Write(buf[:n])
			server.serial.mu.Unlock()
		}
	}
}

func (server *ForkingServer) listen() {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", server.port))
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Fatal(err)
			continue
		}
		go server.handleConnection(conn)
	}
}

func (server *ForkingServer) handleConnection(c net.Conn) {
	defer c.Close()
	a, err := net.ResolveTCPAddr("tcp", c.RemoteAddr().String())
	if err != nil {
		fmt.Println(err)
		return
	}
	ip := a.IP.String()
	server.addrs.mu.Lock()
	server.addrs.v[ip]++
	server.addrs.mu.Unlock()
	server.conns.mu.Lock()
	server.conns.v[c] = true
	server.conns.mu.Unlock()
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
	server.addrs.mu.Lock()
	server.addrs.v[ip]--
	if server.addrs.v[ip] == 0 {
		delete(server.addrs.v, ip)
	}
	server.addrs.mu.Unlock()
	server.conns.mu.Lock()
	delete(server.conns.v, c)
	server.conns.mu.Unlock()
}

func (server *ForkingServer) readFileAndSendToAll() {
	b := make([]byte, 1024)

	for {
		n, err := func() (int, error) {
			server.serial.mu.Lock()
			defer server.serial.mu.Unlock()
			if server.serial.v == nil {
				server.startSerial()
			}
			n, err := server.serial.v.Read(b)
			if err != nil {
				fmt.Println(err)
				server.serial.v.Close()
				server.serial.v = nil
				return 0, err
			}
			return n, nil
		}()
		if err != nil {
			continue
		}

		server.conns.mu.Lock()
		cs := len(server.conns.v)
		server.conns.mu.Unlock()

		server.num_clients.Store(uint32(cs))
		server.num_packets.Add(1)

		server.conns.mu.Lock()
		for conn := range server.conns.v {
			conn.Write(b[:n])
			udpBroadcast(&server.udp, conn.RemoteAddr(), b[:n])
		}
		server.conns.mu.Unlock()
	}
}

func (server *ForkingServer) startWebServer() {
	log.Printf("Starting web server on port %d\n", server.webport)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		server.addrs.mu.Lock()
		jdata, err := json.Marshal(map[string]interface{}{
			"addrs":       server.addrs.v,
			"num_clients": server.num_clients.Load(),
			"num_packets": server.num_packets.Load(),
		})
		server.addrs.mu.Unlock()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(jdata)
	})
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", server.webport), nil))
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
