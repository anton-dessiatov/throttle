# TCP Bandwidth Throttler

# Launching it

```
go build && ./throttle
```

# Configuration

Configuration is a map of tunnels. For each tunnel map key is a listening tcp
port specification (as defined by net.Listen) and value is JSON object with
fields "connectTo", "tunnelLimit" and "connectionLimit". For each inbound
connection to listening tcp port, throttle app opens outbound connection to
an address specified by "connectTo" and forwards traffic to it.

There are two limits associated with each tunnel - "tunnel limit" and
"connection limit". "Tunnel limit" specifies the throughput to never exceed
by all tunnel connections altogether. "Connection limit" is the throughput
to never exceed by any individual connection that belongs to a given tunnel.

Be aware that throughput is limited based on both inbound and outgoing traffic
(e.g if you have 50Kbps limit for connection and you have 20Kbps inbound stream,
outbound will get limited to 30Kbps).

To reload config, change configuration file and send SIGUSR2 to an application:
```
ps aux | grep throttle
kill -12 <pid of throttle>
```

# Remarks

 * I'd prefer using fsnotify instead of unobvious reloading upon SIGUSR2 signal,
   but according to assignment I cannot use anything beyond golang.org/x/*
