module github.com/lesismal/nbio

go 1.16

require github.com/lesismal/llib v1.1.13

retract (
	v1.5.4 // contains body length parsing bug
)
