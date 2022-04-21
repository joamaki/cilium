A type-safe event bus in Go 1.18
--------------------------------

This is an experiment to implement a low-ceremony event bus implementation that
leverages Go generics to get rid of need for type switches:

```go
  type MyEvent struct {
    Payload string
  }

  builder := NewEventBusBuilder()

  publishMyEvent, err := builder.Register[*MyEvent](
    builder,
    "MyEvent signals something interesting" // Description of the purpose of this event
  )
  // ...

  unsubscribe := builder.Subscribe(
    builder,
    "print MyEvent",  // Description of why we're consuming this event
    func(ev *MyEvent) error {
      fmt.Printf("event received: %s\n", ev.Payload)
      return nil
    }
  )

  bus, err := builder.Build()
  if err != nil { ... }

  publishMyEvent(&MyEvent{"hello"})
```

For performance and ease of reasoning, the event bus after built is mostly immutable,
except subscribers can unsubscribe. At built time one can subscribe to events before
they're registered, allowing flexibility in the order in which system components are
initialized.

Benchmark results against a straight function pointer invocation:
```
$ go test -bench .
goos: linux
goarch: amd64
pkg: github.com/joamaki/genbus
cpu: AMD Ryzen 9 5950X 16-Core Processor
BenchmarkGenbus-32              213341856                5.525 ns/op
BenchmarkFuncCall-32            940536712                1.273 ns/op
PASS
ok      github.com/joamaki/genbus       3.079s
```
