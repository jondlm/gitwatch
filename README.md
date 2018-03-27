# gitwatch

`gitwatch` is a small process that will clone and poll a git repo for changes.
When new changes are detected it will invoke the command you provide it.

## Install

You can download binaries for linux/mac from the [release
page](https://github.com/jondlm/gitwatch/releases/latest). Or if you have
golang installed:

    go get github.com/jondlm/gitwatch

## Usage

See `gitwatch -h` for more help info.

## Example

    gitwatch --repo ssh://git@foo.com/foo.git -- echo saw an update
