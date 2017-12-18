package udp

// OptEvent defines an handler used to provide events.
type OptEvent struct {
	Event func(event string, format string, a ...interface{})
}

// Config provides a data structure of required configuration parameters.
type Config struct {
	NetType string // "udp", udp4" or "udp6"
	Addr    string // "host:port" or "[ipv6-host%zone]:port"

	ConnHandler ConnHandler // Support for binding new connections to a reader and writer.
	ReqHandler  ReqHandler  // Support for handling the specific request workflow.
	RespHandler RespHandler // Support for handling the specific response workflow.

	// *************************************************************************
	// ** Not Required, optional                                              **
	// *************************************************************************

	OptEvent
}

// Validate checks the configuration to required items.
func (cfg *Config) Validate() error {
	if cfg == nil {
		return ErrInvalidConfiguration
	}

	if cfg.NetType != "udp" && cfg.NetType != "udp4" && cfg.NetType != "udp6" {
		return ErrInvalidNetType
	}

	if cfg.ConnHandler == nil {
		return ErrInvalidConnHandler
	}

	if cfg.ReqHandler == nil {
		return ErrInvalidReqHandler
	}

	if cfg.RespHandler == nil {
		return ErrInvalidRespHandler
	}

	return nil
}

// Event fires events back to the user for important events.
func (cfg *Config) Event(event string, format string, a ...interface{}) {
	if cfg.OptEvent.Event != nil {
		cfg.OptEvent.Event(event, format, a...)
	}
}
