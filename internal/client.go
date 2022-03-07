package internal

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"runtime/debug"
	"sort"
	"time"
)

type SyncState uint

const (
	SyncStateNone      SyncState = 0
	SyncStateStart     SyncState = 1
	SyncStateSync      SyncState = 2
	SyncStateKeepAlive SyncState = 3
)

type Client struct {
	Address        *net.UDPAddr
	TickOffset     int16
	Connection     *net.UDPConn
	Instance       *Instance
	JoinedTime     time.Time
	LastPacketTime time.Time
	Ping           uint16
	Session        *Session
	SessionSlot    byte
	ControlSeq     uint16
	SyncStopped    bool
	SyncState      SyncState
	SyncDelay      uint16
	Peers          map[byte]*Client
}

func NewClient(instance *Instance, conn *net.UDPConn, address *net.UDPAddr, initTick uint16) *Client {
	return &Client{
		Address:        address,
		Connection:     conn,
		TickOffset:     int16(initTick - instance.GetServerTick()),
		Instance:       instance,
		JoinedTime:     time.Now(),
		LastPacketTime: time.Now(),
		ControlSeq:     0,
		SyncState:      SyncStateNone,
		Peers:          make(map[byte]*Client),
	}
}

// GetControlSeq gets the client's latest control-packet sequence number.
func (c *Client) GetControlSeq() uint16 {
	tmp := c.ControlSeq
	c.ControlSeq++
	return tmp
}

// Returns the client's remote port number.
func (c *Client) Port() int {
	return c.Address.Port
}

// Sends a data buffer to the client.
func (c *Client) Send(data []byte) (int, error) {
	return c.Connection.WriteToUDP(data, c.Address)
}

// Processes a data packet from the client.
func (c *Client) HandlePacket(data []byte) error {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Fatal error while processing packet!")
			fmt.Println("Client:", c.Address.String())
			fmt.Println("Data:", hex.EncodeToString(data))
			fmt.Println("Error:", r)
			fmt.Println("Stack trace:")
			debug.PrintStack()
		}
	}()

	c.Ping = uint16(time.Now().Sub(c.LastPacketTime).Seconds() * 1000)
	c.LastPacketTime = time.Now()

	if len(data) == 26 && data[3] == 0x07 {
		return c.handleSyncStart(data)
	} else if len(data) == 22 && data[3] == 0x07 {
		return c.handleSync(data)
	} else if len(data) == 18 && data[3] == 0x07 {
		return c.handleKeepAlive(data)
	} else if data[0] == 1 {
		return c.handlePeerToPeer(data)
	} else {
		return fmt.Errorf("cannot determine packet type")
	}
}

func (c *Client) handleKeepAlive(data []byte) error {
	if c.Session == nil {
		return fmt.Errorf("received keep alive from client not in any session")
	}
	c.SyncState = SyncStateKeepAlive
	c.Session.IncrementSyncCount()
	return nil
}

func (c *Client) handlePeerToPeer(data []byte) error {
	if c.Session == nil {
		return fmt.Errorf("received P2P message from client not in any session")
	}

	//if !c.Session.Ready {
	//	// todo: should we bother returning an error here? this is technically expected behavior
	//	return fmt.Errorf("received P2P message from client in session that's not ready")
	//}

	for i := 1; i < len(data)-1; {
		peerId := data[i]
		peerMsgSize := int(binary.BigEndian.Uint16(data[i+1 : i+3]))
		peerMsg := data[i+3 : i+3+peerMsgSize]

		peerClient, peerClientExists := c.Peers[peerId]

		if peerClientExists {
			transformedForPeer := peerClient.transformPeerPacket(c, peerMsg)
			_, sendErr := peerClient.Send(transformedForPeer)

			if sendErr != nil {
				return sendErr
			}
		}

		i += 3 + peerMsgSize
	}

	return nil
}

func (c *Client) transformPeerPacket(sender *Client, data []byte) []byte {
	newPacket := make([]byte, len(data)+2)
	newPacket[0] = 1
	newPacket[1] = sender.SessionSlot
	copy(newPacket[2:] /*fixPostPacket(c, sender, data)*/, data)

	return newPacket
}

// Handles a SYNC-START packet from the client.
func (c *Client) handleSyncStart(data []byte) error {
	c.SyncState = SyncStateStart
	sessionId := binary.BigEndian.Uint32(data[16:20])
	slotByte := data[20]

	session, exists := c.Instance.Sessions[sessionId]

	if !exists {
		c.Instance.Sessions[sessionId] = NewSession(sessionId, (slotByte&0x0F)>>1)
		session = c.Instance.Sessions[sessionId]
	}

	c.SessionSlot = slotByte >> 5
	c.Session = session

	if _, inSession := session.Clients[c.SessionSlot]; !inSession {
		session.Clients[c.SessionSlot] = c
		session.ClientCount++
		if session.ClientCount == session.MaxClients {
			for _, client := range session.Clients {
				fmt.Printf("Generating peers for client %s\n", client.Address.String())
				otherClients := make([]*Client, 0)
				for _, otherClient := range session.Clients {
					if otherClient == client {
						continue
					}
					otherClients = append(otherClients, otherClient)
				}
				sort.SliceStable(otherClients, func(i, j int) bool {
					return otherClients[i].SessionSlot < otherClients[j].SessionSlot
				})
				for i, otherClient := range otherClients {
					fmt.Printf("\tPeer %d is %s\n", i, otherClient.Address.String())
					client.Peers[byte(i)] = otherClient
				}
			}

			session.Ready = true
		}
	}

	session.IncrementSyncCount()

	return nil
}

func (c *Client) handleSync(data []byte) error {
	if c.Session == nil {
		return fmt.Errorf("received loading state update from client not in any session")
	}

	if c.SyncStopped {
		c.SyncState = SyncStateSync
		c.Session.IncrementSyncCount()
	} else {
		c.SyncStopped = true
	}

	return nil
}

func (c *Client) SendSyncResponse() {
	switch c.SyncState {
	case SyncStateStart:
		c.SendSyncStart()
		break
	case SyncStateSync:
		c.SendSync()
		break
	case SyncStateKeepAlive:
		c.SendKeepAlive()
		break
	}

	c.SyncState = SyncStateNone
}

// Sends a HELLO response packet to the client.
// The packet is 12 bytes long.
func (c *Client) SendHelloResponse() (int, error) {
	buffer := &bytes.Buffer{}

	// First packet type
	buffer.WriteByte(0)
	// Counter
	binary.Write(buffer, binary.BigEndian, c.GetControlSeq())
	// Packet flag (only tick diff)
	buffer.WriteByte(1)
	// Server tick
	serverTick := c.Instance.GetServerTick()
	binary.Write(buffer, binary.BigEndian, serverTick)
	// Tick diff
	binary.Write(buffer, binary.BigEndian, c.TickOffset)
	// CRC
	buffer.Write([]byte{0x01, 0x01, 0x01, 0x01})

	return c.Send(buffer.Bytes())
}

func (c *Client) SendSync() (int, error) {
	buffer := &bytes.Buffer{}

	writeSyncPacketHeader(c, buffer)

	buffer.WriteByte(0x01) // Message ID: countdown proposal
	buffer.WriteByte(0x03) // Message size: 3 bytes
	buffer.WriteByte(0x00) // unknown
	binary.Write(buffer, binary.LittleEndian, c.Instance.GetServerTick()+3000)
	buffer.WriteByte(0xff)
	buffer.Write([]byte{0x01, 0x01, 0x01, 0x01})

	return c.Send(buffer.Bytes())
}

func (c *Client) SendKeepAlive() (int, error) {
	buffer := &bytes.Buffer{}

	writeSyncPacketHeader(c, buffer)

	buffer.WriteByte(0xff)
	buffer.Write([]byte{0x01, 0x01, 0x01, 0x01})

	return c.Send(buffer.Bytes())
}

// Sends a SYNC-START response packet to the client.
// The packet is 25 bytes long.
func (c *Client) SendSyncStart() (int, error) {
	buffer := &bytes.Buffer{}

	writeSyncPacketHeader(c, buffer)

	// Sub-packet
	buffer.WriteByte(0)
	buffer.WriteByte(6)
	buffer.WriteByte(c.SessionSlot)
	binary.Write(buffer, binary.BigEndian, c.Session.SessionId)

	peerMask := byte(0x00)

	for i := byte(0); i < c.Session.MaxClients; i++ {
		peerMask |= 1 << i
	}

	buffer.WriteByte(peerMask)
	buffer.WriteByte(0xff)
	buffer.Write([]byte{0x01, 0x01, 0x01, 0x01})

	return c.Send(buffer.Bytes())
}

func writeSyncPacketHeader(c *Client, buffer *bytes.Buffer) {
	// First packet type
	buffer.WriteByte(0)
	// Sequence number
	binary.Write(buffer, binary.BigEndian, c.GetControlSeq())
	// Packet flag (tick diff + sync bits)
	buffer.WriteByte(2)
	// Server tick
	serverTick := c.Instance.GetServerTick()
	binary.Write(buffer, binary.BigEndian, serverTick)
	// Tick diff
	binary.Write(buffer, binary.BigEndian, c.TickOffset)
	// Sync-counter
	binary.Write(buffer, binary.BigEndian, uint16(c.Session.SyncCount))
	binary.Write(buffer, binary.BigEndian, uint16(0xFFFF)&^(1<<(16-c.Session.SyncCount)))
}
