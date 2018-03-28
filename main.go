package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
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
	gitPlumbing "gopkg.in/src-d/go-git.v4/plumbing"
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
	branch          string
	slackWebhook    string
	slackTitle      string
	args            []string
	intervalSeconds int
	endOfTimes      chan error
	destDir         string
}

type slackMessageField struct {
	Title string `json:"title"`
	Value string `json:"value"`
}

type slackMessage struct {
	Fallback string              `json:"fallback"`
	Pretext  string              `json:"pretext"`
	Color    string              `json:"color"`
	Fields   []slackMessageField `json:"fields"`
}

func main() {
	app := cli.App("gitwatch", "Watch a git repo and execute a command on updates. Currently only supports ssh authentication.")

	app.Spec = "[-v] [--slack-webhook] [--slack-title] [--interval-seconds] [--key] [--repo] [--dir] [--branch] CMD [ARG...]"
	app.Version("version", version)

	var (
		gitRepo         = app.StringOpt("repo", "", "git repo to watch")
		verbose         = app.BoolOpt("v verbose", false, "verbose logging")
		intervalSeconds = app.IntOpt("interval-seconds", 30, "seconds gitwatch will wait between checks")
		destDir         = app.StringOpt("dir", "", "directory where the git repo will be cloned. If not provided, gitwatch will create a temporary directory that it will clean up when finished")
		key             = app.StringOpt("key", "", "location of ssh private key")
		branch          = app.StringOpt("branch", "master", "git branch to clone and watch")
		slackWebhook    = app.StringOpt("slack-webhook", "", "slack webhook URL to send notifications about invocations to")
		slackTitle      = app.StringOpt("slack-title", "", "the title that the slack webhook should report when sending messages, this should be a name that can help people identify where this process is running")
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

			slackTitle:      *slackTitle,
			slackWebhook:    *slackWebhook,
			branch:          *branch,
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
	auth := git.CloneOptions{}.Auth
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
		URL:           ctx.gitRepo,
		Progress:      os.Stdout,
		Auth:          auth,
		ReferenceName: gitPlumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", ctx.branch)),
		SingleBranch:  true,
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
			Progress:      os.Stdout,
			Auth:          auth,
			ReferenceName: gitPlumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", ctx.branch)),
			SingleBranch:  true,
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

	slackColor := "good"
	c := exec.Command(ctx.cmd, ctx.args...)
	output, err := c.CombinedOutput()
	if err != nil {
		ctx.log.Error("error while running command")
		ctx.log.Error(err)
		slackColor = "bad"
	} else {
		log.Info("success")
	}
	fmt.Printf(string(output))

	if ctx.slackWebhook != "" {
		json, err := json.Marshal(slackMessage{
			Fallback: ctx.slackTitle,
			Pretext:  ctx.slackTitle,
			Color:    slackColor,
			Fields: []slackMessageField{
				slackMessageField{
					Title: "stdout and stderr",
					Value: fmt.Sprintf("```%s```", string(output)),
				},
			},
		})

		req, err := http.NewRequest("POST", ctx.slackWebhook, bytes.NewBuffer(json))
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			ctx.log.Warnf("unable to send slack notification: %v", err)
		}
		if resp.StatusCode != 200 {
			body, _ := ioutil.ReadAll(resp.Body)
			ctx.log.Warnf("got non 200 from slack: %s", body)
		}
		defer resp.Body.Close()
	}

	return err
}

func derefArgs(args []string) []string {
	newArgs := []string{}

	for _, a := range args {
		newArgs = append(newArgs, a)
	}

	return newArgs
}
