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
	//"path"

	"github.com/jawher/mow.cli"
	"github.com/sirupsen/logrus"
	//"golang.org/x/crypto/ssh"
	git "gopkg.in/src-d/go-git.v4"
	//gitSSH "gopkg.in/src-d/go-git.v4/plumbing/transport/ssh"
)

// This gets set by `go build -ldflags "-X main.version=1.0.0"`
var version string
var log *logrus.Logger

type context struct {
	log             *logrus.Logger
	gitRepo         string
	cmd             string
	args            []string
	intervalSeconds int
	endOfTimes      chan error
}

func main() {
	app := cli.App("gitwatch", "Watch a git repo and execute a command on updates")

	app.Spec = "[-v] [--interval-seconds] GIT_REPO -- CMD [ARG...]"
	app.Version("version", version)

	var (
		gitRepo         = app.StringArg("GIT_REPO", "", "git repo to watch")
		cmd             = app.StringArg("CMD", "", "command to invoke")
		args            = app.StringsArg("ARG", []string{}, "argument(s) to the command")
		verbose         = app.BoolOpt("v verbose", false, "verbose logging")
		intervalSeconds = app.IntOpt("interval-seconds", 30, "seconds gitwatch will wait between checks")
		gracefulStop    = make(chan os.Signal)
		endOfTimes      = make(chan error)
	)

	// Register our listener for a SIGINT with the `gracefulStop` channel
	signal.Notify(gracefulStop, os.Interrupt)
	go func() {
		<-gracefulStop
		endOfTimes <- errors.New("stopping because of an interrupt signal")
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
			log:             log,
			gitRepo:         *gitRepo,
			cmd:             *cmd,
			args:            derefArgs(*args),
			intervalSeconds: *intervalSeconds,
			endOfTimes:      endOfTimes,
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
	tmpDir, err := ioutil.TempDir("", "")
	if err != nil {
		ctx.endOfTimes <- err
		return
	}
	defer os.RemoveAll(tmpDir)

	ctx.log.Infof("cloning to %s", tmpDir)
	repo, err := git.PlainClone(tmpDir, false, &git.CloneOptions{
		URL:      ctx.gitRepo,
		Progress: os.Stdout,
	})
	if err != nil {
		ctx.endOfTimes <- err
		return
	}
	runCommand(ctx)

	for {
		ctx.log.Info("pulling")
		worktree, err := repo.Worktree()
		if err != nil {
			ctx.endOfTimes <- err
			return
		}

		err = worktree.Pull(&git.PullOptions{
			Progress: os.Stdout,
		})
		switch err {
		case git.NoErrAlreadyUpToDate:
			ctx.log.Info("repo already up to date, nothing to do")
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
