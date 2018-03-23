# lazywatch

`lazywatch` is a small CLI tool that will watch a directory and lazily invoke
the command you provide it. Currently it will debounce file events by 1 second
and only invoke the command if it's not already running.

## Install

    go get github.com/jondlm/lazywatch

## Usage

See `lazywatch -h` for more help info.

## Example

    lazywatch /tmp/mydir echo woot
