package engine

import (
	"bufio"
	"context"
	"io"
	"net/http/httputil"
	"time"

	"github.com/docker/docker/pkg/stdcopy"

	dockertypes "github.com/docker/docker/api/types"
	coreutils "github.com/projecteru2/core/utils"
	log "github.com/sirupsen/logrus"

	"github.com/jschwinger23/bufpipe"
	"github.com/projecteru2/agent/common"
	"github.com/projecteru2/agent/engine/logs"
	"github.com/projecteru2/agent/types"
	"github.com/projecteru2/agent/watcher"
)

func (e *Engine) attach(container *types.Container) {
	transfer := e.forwards.Get(container.ID, 0)
	if transfer == "" {
		transfer = logs.Discard
	}
	writer, err := logs.NewWriter(transfer, e.config.Log.Stdout)
	if err != nil {
		log.Errorf("[attach] Create log forward failed %s", err)
		return
	}

	outr, outw := bufpipe.New(nil, 10*1024*1024)
	errr, errw := bufpipe.New(nil, 10*1024*1024)
	ctx := context.Background()
	cancelCtx, cancel := context.WithCancel(ctx)
	go func() {
		options := dockertypes.ContainerAttachOptions{
			Stream: true,
			Stdin:  false,
			Stdout: true,
			Stderr: true,
		}
		resp, err := e.docker.ContainerAttach(ctx, container.ID, options)
		if err != nil && err != httputil.ErrPersistEOF {
			log.Errorf("[attach] attach %s container %s failed %s", container.Name, coreutils.ShortID(container.ID), err)
			return
		}
		defer resp.Close()
		defer outw.Close()
		defer errw.Close()
		defer cancel()
		_, err = stdcopy.StdCopy(outw, errw, resp.Reader)
		if err != nil {
			log.Errorf("[attach] attach get stream failed %s", err)
		}
		log.Infof("[attach] attach %s container %s finished", container.Name, coreutils.ShortID(container.ID))
	}()
	log.Infof("[attach] attach %s container %s success", container.Name, coreutils.ShortID(container.ID))
	// attach metrics
	go e.stat(cancelCtx, container)
	pump := func(typ string, source io.Reader) {
		buf := bufio.NewScanner(source)
		for buf.Scan() {
			data := buf.Text()
			//			data = strings.TrimSuffix(data, "\n")
			//			data = strings.TrimSuffix(data, "\r")
			l := &types.Log{
				ID:         container.ID,
				Name:       container.Name,
				Type:       typ,
				EntryPoint: container.EntryPoint,
				Ident:      container.Ident,
				Data:       data,
				Datetime:   time.Now().Format(common.DateTimeFormat),
				//TODO
				//Extra
			}
			watcher.LogMonitor.LogC <- l
			if err := writer.Write(l); err != nil && !(container.EntryPoint == "agent" && e.dockerized) {
				log.Errorf("[attach] %s container %s_%s write failed %v", container.Name, container.EntryPoint, coreutils.ShortID(container.ID), err)
				log.Errorf("[attach] %s", data)
			}
		}
		if err := buf.Err(); err != nil {
			log.Errorf("[attach] attach pump %s %s %s %s", container.Name, coreutils.ShortID(container.ID), typ, err)
		}
		log.Infof("[attach] %s %s forwarding done", container.ID, typ)
	}
	go pump("stdout", outr)
	go pump("stderr", errr)
}
