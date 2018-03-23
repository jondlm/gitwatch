# gitwatch

`gitwatch` is a small process that will clone and poll a git repo for changes.
When new changes are detected it will invoke the command you provide it.

## Install

    go get github.com/jondlm/gitwatch

## Usage

See `gitwatch -h` for more help info.

## Example

    gitwatch ssh://git@foo.com/foo.git -- echo saw an update
