package app

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// ListenAt is a type for listening specifications compatible with net.Listen
// function.
type ListenAt string

// UnmarshalJSON is an implementation of json.Unmarshaler for ListenAt
func (x *ListenAt) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	*x = ListenAt(s)
	return nil
}

// ConnectTo is a type for connection destination specification compatible with
// net.Dial function.
type ConnectTo string

// UnmarshalJSON is an implementation of json.Unmarshaler for ConnectTo
func (x *ConnectTo) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	*x = ConnectTo(s)
	return nil
}

// Limit is a bandwidth limit expressed in bytes per second.
type Limit int64

// uom stands for Unit Of Measurement. Units are BITS per second, not bytes
var uomSuffixes = []struct {
	unit string
	mul  int64
	div  int64
}{
	// IPerf assumes that megabit per second is exactly 1000000 bits per second
	// (not 1024 * 1024)
	{unit: "Kbps", mul: 1000, div: 8},
	{unit: "Mbps", mul: 1000 * 1000, div: 8},
	{unit: "Gbps", mul: 1000 * 1000 * 1000, div: 8},
	// tcptrack, on the other hand, uses <prefix>bytes per second where prefix
	// is a power of 2, that's why I'm using powers of 1024 for bytes-per-second
	// units
	{unit: "KBps", mul: 1024, div: 1},
	{unit: "MBps", mul: 1024 * 1024, div: 1},
	{unit: "GBps", mul: 1024 * 1024 * 1024, div: 1},
	{unit: "bps", mul: 1, div: 8},
	{unit: "Bps", mul: 1, div: 1},
}

// Tries to parse an UOM suffix from a string. Returns string stripped from that
// suffix and a multiplier. If no suffix matches, returns string as is and 1 as
// a multiplier.
func parseSuffix(s string) (string, int64, int64) {
	for _, v := range uomSuffixes {
		if strings.HasSuffix(s, v.unit) {
			return s[0 : len(s)-len(v.unit)], v.mul, v.div
		}
	}

	return s, 1, 1
}

// UnmarshalJSON is an implementation of json.Unmarshaler for Limit
func (x *Limit) UnmarshalJSON(data []byte) error {
	var bytesPerSecond int64
	err := json.Unmarshal(data, &bytesPerSecond)
	if err == nil {
		// We have successfully unmarshaled an int64. Assume it's bytes per second.
	} else {
		// Okay, we might be dealing with a suffix. Let's check.
		var s string
		err = json.Unmarshal(data, &s)
		if err != nil {
			// Whoa! Something is seriously odd.
			return err
		}

		numberString, mul, div := parseSuffix(s)
		bytesPerSecond, err = strconv.ParseInt(numberString, 10, 64)
		if err != nil {
			return fmt.Errorf("Failed to parse %q", s)
		}
		bytesPerSecond *= mul
		bytesPerSecond /= div

		if bytesPerSecond < 0 {
			return fmt.Errorf("Negative values are not accepted as a bandwidth limit (%q)", s)
		}
	}

	if bytesPerSecond < 0 {
		return fmt.Errorf("Negative values are not accepted as a bandwidth limit (%q)", bytesPerSecond)
	}

	*x = Limit(bytesPerSecond)
	return nil
}

// ConfigurationJSON encapsulates application confituration as defined in
// configuration file
type ConfigurationJSON struct {
	Tunnels map[ListenAt]TunnelConfigJSON `json:"tunnels"`
}

// TunnelConfigJSON encapsulates configuration of an individual tunnel as
// defined in configuration file
type TunnelConfigJSON struct {
	ConnectTo       ConnectTo `json:"connectTo"`
	TunnelLimit     Limit     `json:"tunnelLimit"`
	ConnectionLimit Limit     `json:"connectionLimit"`
}

// LoadAndWatch loads configuration from a given path, pushes it onto a
// configUpdate channel and starts listening for SIGUSR2 signals to reload
// config until quit channel gets closed. Upon each SIGUSR2 configuration is
// reloaded and sent to configUpdate.
func LoadAndWatch(path string, configUpdate chan<- ConfigurationJSON, gs *gracefulShutdown) error {
	initial, err := load(path)
	if err != nil {
		return err
	}

	configUpdate <- initial

	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGUSR2)

	gs.waitGroup.Add(1)

	go func() {
		defer gs.waitGroup.Done()
		for {
			select {
			case <-s:
				updated, err := load(path)
				if err != nil {
					log.Printf("Failed to reload configuration from %q: %v\n", path, err)
				} else {
					configUpdate <- updated
				}
			case <-gs.quit:
				log.Printf("Stopped watching the file")
				return
			}
		}
	}()

	return nil
}

func load(path string) (ConfigurationJSON, error) {
	contents, err := ioutil.ReadFile(path)
	if err != nil {
		log.Printf("Failed to read configuration file at %q: %v\n", path, err)
		return ConfigurationJSON{}, err
	}

	temp := new(ConfigurationJSON)

	if err = json.Unmarshal(contents, temp); err != nil {
		log.Printf("Failed to parse configuration file at %q: %v\n", path, err)
		return ConfigurationJSON{}, err
	}

	return *temp, nil
}
