package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/jawher/mow.cli"
	"github.com/sirupsen/logrus"
	git "gopkg.in/src-d/go-git.v4"
	//gitTransport "gopkg.in/src-d/go-git.v4/plumbing/transport"
	//"golang.org/x/crypto/ssh"
	gitSSH "gopkg.in/src-d/go-git.v4/plumbing/transport/ssh"
)

// This gets set by `go build -ldflags "-X main.version=1.0.0"`
var version string
var log *logrus.Logger

type context struct {
	log             *logrus.Logger
	gitRepo         string
	key             string
	cmd             string
	args            []string
	intervalSeconds int
	endOfTimes      chan error
	destDir         string
}

func main() {
	app := cli.App("gitwatch", "Watch a git repo and execute a command on updates. Currently only supports ssh authentication.")

	app.Spec = "[-v] [--interval-seconds] [--key] [--repo] [--dir] CMD [ARG...]"
	app.Version("version", version)

	var (
		gitRepo         = app.StringOpt("repo", "", "git repo to watch")
		verbose         = app.BoolOpt("v verbose", false, "verbose logging")
		intervalSeconds = app.IntOpt("interval-seconds", 30, "seconds gitwatch will wait between checks")
		destDir         = app.StringOpt("dir", "", "directory where the git repo will be cloned. If not provided, gitwatcher will create a temporary directory that it will clean up when finished")
		key             = app.StringOpt("key", "", "location of ssh private key")
		gracefulStop    = make(chan os.Signal)
		endOfTimes      = make(chan error)
		cmd             = app.StringArg("CMD", "", "command to invoke")
		args            = app.StringsArg("ARG", []string{}, "argument(s) to the command")
	)

	// Register our listener for a SIGINT with the `gracefulStop` channel
	signal.Notify(gracefulStop, os.Interrupt)
	go func() {
		<-gracefulStop
		endOfTimes <- errors.New("stopping due to an interrupt signal")
	}()

	app.Before = func() {
		log = logrus.New()
		log.Formatter = new(logrus.TextFormatter)
		log.Out = os.Stdout

		if *verbose {
			log.Level = logrus.DebugLevel
		} else {
			log.Level = logrus.InfoLevel
		}
	}

	app.Action = func() {
		ctx := &context{
			log:        log,
			endOfTimes: endOfTimes,

			key:             *key,
			gitRepo:         *gitRepo,
			intervalSeconds: *intervalSeconds,
			destDir:         *destDir,
			cmd:             *cmd,
			args:            derefArgs(*args),
		}

		go watchRepo(ctx)

		err := <-endOfTimes
		if err != nil {
			log.Error(err)
			// Use cli.Exit so we give mow.cli a change to run its hooks
			cli.Exit(1)
		}
		cli.Exit(0)
	}

	app.Run(os.Args)
}

func watchRepo(ctx *context) {
	var auth *gitSSH.PublicKeys
	var err error
	dir := ctx.destDir

	if dir == "" {
		tmpDir, err := ioutil.TempDir("", "")
		if err != nil {
			ctx.endOfTimes <- err
			return
		}
		defer os.RemoveAll(tmpDir)
		dir = tmpDir
	}

	// Create the directory if it doesn't exist
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		ctx.log.WithFields(logrus.Fields{"dir": dir}).Info("directory not found, creating it now")
		err = os.MkdirAll(dir, 0644)
		if err != nil {
			ctx.endOfTimes <- err
			return
		}
	}

	ctx.log.Infof("cloning to %s", dir)

	if ctx.key != "" {
		auth, err = gitSSH.NewPublicKeysFromFile("", ctx.key, "")
		if err != nil {
			ctx.endOfTimes <- err
			return
		}
	}

	repo, err := git.PlainClone(dir, false, &git.CloneOptions{
		URL:      ctx.gitRepo,
		Progress: os.Stdout,
		Auth:     auth,
	})
	if err != nil {
		ctx.endOfTimes <- err
		return
	}
	runCommand(ctx)

	for {
		ctx.log.WithFields(logrus.Fields{"gitRepo": ctx.gitRepo}).Debug("pulling")
		worktree, err := repo.Worktree()
		if err != nil {
			ctx.endOfTimes <- err
			return
		}

		err = worktree.Pull(&git.PullOptions{
			Progress: os.Stdout,
			Auth:     auth,
		})
		switch err {
		case git.NoErrAlreadyUpToDate:
			ctx.log.Debug("repo already up to date, nothing to do")
		case nil:
			ctx.log.Info("fetched new updates")
			runCommand(ctx)
		default:
			ctx.endOfTimes <- err
			return
		}

		ctx.log.Debugf("waiting for %d seconds", ctx.intervalSeconds)
		time.Sleep(time.Duration(ctx.intervalSeconds*1000) * time.Millisecond)
	}
}

func runCommand(ctx *context) error {
	ctx.log.WithFields(logrus.Fields{"command": strings.Join(append([]string{ctx.cmd}, ctx.args...), " ")}).Info("running command")
	c := exec.Command(ctx.cmd, ctx.args...)
	output, err := c.CombinedOutput()
	if err != nil {
		ctx.log.Error("error while running command")
		ctx.log.Error(err)
	} else {
		log.Info("success")
	}
	fmt.Printf(string(output))
	return err
}

func derefArgs(args []string) []string {
	newArgs := []string{}

	for _, a := range args {
		newArgs = append(newArgs, a)
	}

	return newArgs
}
