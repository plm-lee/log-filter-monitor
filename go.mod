module log-filter-monitor

go 1.24.0

toolchain go1.24.11

require (
	github.com/hpcloud/tail v0.0.0-00010101000000-000000000000
	gopkg.in/yaml.v2 v2.4.0
)

replace github.com/hpcloud/tail => github.com/hpcloud/tail v0.0.0-20180514194441-a1dbeea552b7

require (
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	gopkg.in/fsnotify/fsnotify.v1 v1.4.7 // indirect
	gopkg.in/tomb.v1 v1.0.0-20141024135613-dd632973f1e7 // indirect
)
