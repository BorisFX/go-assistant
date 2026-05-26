package valueobject

import "fmt"

type SessionKey struct {
	Agent   string
	Channel string
	PeerID  string
}

func NewSessionKey(channel, peerID string) SessionKey {
	return SessionKey{
		Agent:   "assistant",
		Channel: channel,
		PeerID:  peerID,
	}
}

func (s SessionKey) String() string {
	return fmt.Sprintf("%s:%s:%s", s.Agent, s.Channel, s.PeerID)
}
