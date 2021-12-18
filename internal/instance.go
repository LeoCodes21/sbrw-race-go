package internal

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"sync"
)

// Start the UDP server and begin listening for packets
func Start(addrStr string) (*Instance, error) {
	addr, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		return nil, err
	}
	listener, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	i := NewInstance(listener)

	go i.RunPacketRead()

	return i, nil
}

func NewInstance(listener *net.UDPConn) *Instance {
	return &Instance{
		listener: listener,
		buffer:   make([]byte, 1024),
		Clients:  make(map[int]*Client),
		Sessions: make(map[uint32]*Session),
	}
}

type Instance struct {
	sync.Mutex
	listener *net.UDPConn
	buffer   []byte
	Clients  map[int]*Client
	Sessions map[uint32]*Session
}

func (i *Instance) RunPacketRead() {
	for {
		addr, data, err := i.readPacket()

		if err != nil {
			panic(fmt.Sprintf("Error while trying to read packet from listener: %s", err))
		}

		i.Lock()

		if data[0] == 0x00 && data[3] == 0x06 && len(data) == 75 {
			i.Clients[addr.Port] = NewClient(i, i.listener, addr, binary.BigEndian.Uint16(data[69:71]))
			_, err = i.Clients[addr.Port].SendHelloResponse()
		} else {
			if _, exists := i.Clients[addr.Port]; exists {
				err = i.Clients[addr.Port].HandlePacket(data)
			} else {
				err = fmt.Errorf("unknown client")
			}
		}

		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Error while processing packet: %s\n", err)
		}

		i.Unlock()
	}
}

func (i *Instance) readPacket() (*net.UDPAddr, []byte, error) {
	pktLen, addr, err := i.listener.ReadFromUDP(i.buffer)
	return addr, i.buffer[:pktLen], err
}
