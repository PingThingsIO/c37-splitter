# C37 Splitter

This minimalist splitter for C37 streams can receive or make TCP connections from a PMU and then forward that traffic to one or more downstream services by either accepting or making connections to them.

To start the splitter, run:

```
./c37splitter <config>
```

A typical configuration file may look like:

```
# true if we initiate the connection to upstream, false if we accept it
dialUpstream: true
# if dialUpstream is true, this is used
upstreamDialAddress: 10.4.1.70:4713
# if dialUpstream is false, this is used
upstreamListenAddress: 0.0.0.0:4713

# true if we support incoming connections for downstream
listenDownstream: true
# used if the above is true
downstreamListenAddress: 0.0.0.0:4714
# in addition to the above, these addresses, if provided, will be dialed
# for downstream relay
dialDownstreamAddresses:
 - 127.0.0.1:5555
 - 127.0.0.1:5556
```
