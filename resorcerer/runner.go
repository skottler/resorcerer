package resorcerer

import (
	"fmt"
	"github.com/vektra/resorcerer/procstats"
	"github.com/vektra/resorcerer/upstart"
	"os"
	"time"
)

type Work struct {
	job       *upstart.Job
	pid       procstats.Pid
	srv       *Service
	memMetric *Metric
}

func show(gs *procstats.GroupStats) {
	fmt.Printf("%d: %d (%d) - %s\n", gs.Process.Pid, gs.Process.RSS, gs.TotalRSS(), gs.Process.CmdLine)
	for _, x := range gs.Children {
		show(x)
	}
}

var Debug bool = false

const defaultPollSeconds = 5
const defaultPollSamples = 5

func RunLoop(u *upstart.Conn, c *Config) error {
	if c.Poll.Seconds == 0 {
		c.Poll.Seconds = defaultPollSeconds
	}

	if c.Poll.Samples == 0 {
		c.Poll.Samples = defaultPollSamples
	}

	if c.Poll.Significant == 0 {
		c.Poll.Significant = (c.Poll.Samples / 2) + 1
	}

	e := NewEventDispatcher(c)
	e.Debug = Debug

	sm := make(ServiceMetrics)

	var work []*Work

	for _, s := range c.Services {
		j, err := u.Job(s.Name)
		if err != nil {
			return err
		}

		s.action = j

		for _, h := range s.Handlers {
			e.Add(s, h.Event, h)
		}

		e.Dispatch(&Event{"monitor/start", s, nil})

		w := &Work{
			job:       j,
			srv:       s,
			memMetric: sm.Add(s, "memory/limit", c.Poll.Samples),
		}

		w.memMetric.Significant = c.Poll.Significant

		if mem := w.srv.Memory; mem != "" {
			bytes, err := mem.Bytes()
			if err != nil {
				continue
			}

			w.memMetric.Limit = procstats.Bytes(bytes)
		}

		work = append(work, w)
	}

	for {
		forest, err := procstats.DiscoverForest()
		if err != nil {
			return err
		}

		for _, w := range work {
			rpid, err := w.job.Pid()
			if err != nil {
				if w.pid == -1 {
					continue
				}

				w.pid = -1

				e.Dispatch(&Event{"monitoring/down", w.srv, nil})
				continue
			}

			pid := procstats.Pid(rpid)

			if w.pid == -1 {
				e.Dispatch(&Event{"monitoring/up", w.srv, nil})
			} else if w.pid != pid {
				e.Dispatch(&Event{"monitoring/pid-change", w.srv, pid})
			}

			w.pid = pid

			if gs, ok := forest.Processes[pid]; ok {
				rss := gs.TotalRSS()
				e.Dispatch(&Event{"memory/measured", w.srv, rss})
				w.memMetric.Add(e, rss)
			} else {
				fmt.Fprintf(os.Stderr, "Unable to find stats for pid %d\n", pid)
			}
		}

		time.Sleep(time.Duration(c.Poll.Seconds) * time.Second)
	}

	return nil
}