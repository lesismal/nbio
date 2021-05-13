module github.com/lesismal/nbio

go 1.16

require (
	github.com/gorilla/websocket v1.4.2
	github.com/julienschmidt/httprouter v1.3.0
	github.com/lesismal/llib v0.0.0-20210227131634-a5b41cb31331
	github.com/valyala/fasthttp v1.24.0
)

replace github.com/lesismal/llib v0.0.0-20210227131634-a5b41cb31331 => github.com/acgreek/llib v0.0.0-20210513153353-2bf9ca9cfa7f
