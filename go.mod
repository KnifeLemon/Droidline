module github.com/KnifeLemon/Droidline

go 1.26.0

ignore (
	./agent
	./relay/worker/node_modules
	./sdk/node/node_modules
)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	rsc.io/qr v0.2.0 // indirect
)
