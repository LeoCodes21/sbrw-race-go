package internal

import (
	"encoding/hex"
	"net"
	"sync"
	"encoding/binary"
	"fmt"
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
		Clients:make(map[int]*Client),
		Sessions:make(map[uint32]*Session),
	}
}

type Instance struct {
	sync.Mutex
	listener *net.UDPConn
	buffer   []byte
	Clients  map[int]*Client
	Sessions  map[uint32]*Session
}

func (i *Instance) RunPacketRead() {
	for {
		addr, data := i.readPacket()
		i.Lock()
		fmt.Printf("Packet from %s (%d bytes):\n", addr.String(), len(data))
		fmt.Println(hex.Dump(data))

		// hello-packet
		if data[0] == 0x00 && data[3] == 0x06 && len(data) == 75 {
			i.Clients[addr.Port] = NewClient(i, i.listener, addr, binary.BigEndian.Uint16(data[69:71]))
			i.Clients[addr.Port].SendHelloResponse()
		} else {
			if _, exists := i.Clients[addr.Port]; exists {
				i.Clients[addr.Port].HandlePacket(data)
			} else {
				fmt.Println("UNKNOWN CLIENT")
			}
		}

		i.Unlock()
	}
}

func (i *Instance) readPacket() (*net.UDPAddr, []byte) {
	pktLen, addr, _ := i.listener.ReadFromUDP(i.buffer)
	return addr, i.buffer[:pktLen]
}
