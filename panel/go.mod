module github.com/aethernet/aethernet/panel

go 1.22

require (
	github.com/aethernet/aethernet/pkg v0.0.0
	github.com/gorilla/websocket v1.5.1
)

replace github.com/aethernet/aethernet/pkg => ../pkg
