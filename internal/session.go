package internal

type Session struct {
	Clients       map[byte]*Client
	ClientCount   byte
	MaxClients    byte
	SessionId     uint32
	SyncCount     uint32
	SyncedClients uint32
}

func NewSession(sessionId uint32, maxClients byte) *Session {
	return &Session{
		Clients:       make(map[byte]*Client, maxClients),
		MaxClients:    maxClients,
		SessionId:     sessionId,
		SyncCount:     0,
		ClientCount:   0,
		SyncedClients: 0,
	}
}

func (s *Session) IncrementSyncCount() {
	s.SyncedClients++

	if s.SyncedClients == uint32(s.MaxClients) {
		s.SyncedClients = 0
		s.SyncCount++

		for _, client := range s.Clients {
			client.SendSyncResponse()
		}
	}
}

func (s *Session) IsAllPlayerInfoBeforeOk() bool {
	for _, client := range s.Clients {
		if !client.IsPlayerInfoBeforeOk() {
			return false
		}
	}

	return true
}