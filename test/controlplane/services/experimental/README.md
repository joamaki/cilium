
# Control-plane tests for pkg/loadbalancer/experimental

These tests check various load-balancer control-plane functions using the
experimental service load-balancing API (experimental.Services) and its
wrapper that intercepts ServiceManager calls and routes them through the
Table[Frontend] and Table[Backend].

The test cases were generated with 'generate.sh'. Based on
the nodeport tests.
