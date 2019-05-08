# TCP Bandwidth Throttler

# Building & launching it

```
export GO111MODULE=on
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

Both connections and tunnel limits should be specified in "<number><uom>" format
where <number> is non-negative and <uom> is one of
  * ```Gbps``` - billions of bits per second
  * ```Mbps``` - millions of bits per second
  * ```Kbps``` - thousands of bits per second
  * ```bps``` - bits per second
  * ```GBps``` - Gigabytes per second
  * ```MBps``` - Megabytes per second
  * ```KBps``` - Kilobytes per second
  * ```Bps``` - bytes per second

If no unit of measure is specified, bytes per second are assumed.

Be aware that throughput is limited based on both inbound and outgoing traffic
(e.g if you have 50Kbps limit for connection and you have 20Kbps inbound stream,
outbound will get limited to 30Kbps).

Application loads configuration from ```config.json``` file in the current
directory.

To reload config, change configuration file and send SIGUSR2 to an application:
```
ps aux | grep throttle
kill -12 <pid of throttle>
```

# Testing

```
# 1st console:
go build && ./throttle
# 2nd console:
iperf -s -p 32166
# 3rd console:
iperf -c localhost -p 32167 -n 100M
```

It might also be useful to start [tcptrack](https://linux.die.net/man/1/tcptrack)
on a nearby console:

```
sudo tcptrack -i lo
```

# Remarks

 * I'd prefer using fsnotify instead of unobvious reloading upon SIGUSR2 signal,
   but according to assignment I cannot use anything beyond golang.org/x/*

 * I'm not checking for zero throughputs because I believe it might actually be
   useful to temporarily suspend connections from a given server.
