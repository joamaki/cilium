package worker

import (
	"context"
	"fmt"

	"github.com/cilium/cilium/pkg/container"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/metrics/metric"
	"github.com/cilium/workerpool"
	"k8s.io/client-go/util/workqueue"
)

// Generic workqueue driven worker that processes requests sequentially.
//
// Implemented this as a generic version as this is a pattern we might want to
// potentially reuse.

type workerMetrics struct {
	TotalProcessed metric.Counter
	TotalRetries   metric.Counter
	Queued         metric.Gauge
	Retried        metric.Gauge
}

func newWorkerMetrics(name string) func() workerMetrics {
	return func() workerMetrics {
		return workerMetrics{
			TotalProcessed: metric.NewCounter(metric.CounterOpts{
				Namespace: "cilium",
				Subsystem: metric.Subsystem{
					// TODO something saner for making up the names
					Name:    "datapath_" + name,
					DocName: "datapath_" + name,
				},
				Name:             "processed_total",
				Help:             "Number of datapath load-balancer requests processed",
				Description:      "Number of datapath load-balancer requests processed",
				EnabledByDefault: true,
				MetricType:       metric.MetricTypeEndpoint, // TODO what's this
			}),
			TotalRetries: metric.NewCounter(metric.CounterOpts{
				Namespace: "cilium",
				Subsystem: metric.Subsystem{
					Name:    "datapath_" + name,
					DocName: "datapath_" + name,
				},
				Name:             "retries_total",
				Help:             "Number of datapath load-balancer requests retried",
				Description:      "Number of datapath load-balancer requests retried",
				EnabledByDefault: true,
				MetricType:       metric.MetricTypeEndpoint, // TODO what's this
			}),
			Queued: metric.NewGauge(metric.GaugeOpts{
				Namespace: "cilium",
				Subsystem: metric.Subsystem{
					Name:    "datapath_" + name,
					DocName: "datapath_" + name,
				},
				Name:             "queued",
				Help:             "Number of datapath load-balancer requests queued",
				Description:      "Number of datapath load-balancer requests queued",
				EnabledByDefault: true,
				MetricType:       metric.MetricTypeEndpoint, // TODO what's this
			}),
			Retried: metric.NewGauge(metric.GaugeOpts{
				Namespace: "cilium",
				Subsystem: metric.Subsystem{
					Name:    "datapath_" + name,
					DocName: "datapath_" + name,
				},
				Name:             "retried",
				Help:             "Number of datapath load-balancer requests currently being retried",
				Description:      "Number of datapath load-balancer requests curretnly being retried",
				EnabledByDefault: true,
				MetricType:       metric.MetricTypeEndpoint, // TODO what's this
			}),
		}
	}
}

type workItem struct {
	key     string
	process func() error
}

type Worker interface {
	Enqueue(key string, process func() error)
}

type worker struct {
	name   string
	params workerParams

	wq workqueue.RateLimitingInterface

	newWorkItems chan workItem
	work         chan string // TODO naming
}

type workerParams struct {
	cell.In

	Lifecycle hive.Lifecycle
	Metrics   workerMetrics
	Reporter  cell.StatusReporter
}

func NewWorkerCell(name string) cell.Cell {
	return cell.Group(
		cell.Metrics(newWorkerMetrics(name)),
		cell.ProvidePrivate(newWorker))
}

func newWorker(p workerParams) Worker {
	w := &worker{
		wq:           workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()), // TODO parametrize
		params:       p,
		newWorkItems: make(chan workItem),
		work:         make(chan string),
	}
	wp := workerpool.New(2)
	p.Lifecycle.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			if err := wp.Submit("queueGetter", w.queueGetter); err != nil {
				wp.Close()
				return err
			}
			if err := wp.Submit("processLoop", w.processLoop); err != nil {
				wp.Close()
				return err
			}
			return nil
		},
		OnStop: func(hive.HookContext) error {
			defer p.Reporter.Down("Stopped")
			return wp.Close()
		},
	})
	return w
}

func (w *worker) Enqueue(key string, process func() error) {
	w.newWorkItems <- workItem{key, process}
}

func (w *worker) queueGetter(context.Context) error {
	defer close(w.work)
	for {
		raw, shutdown := w.wq.Get()
		if shutdown {
			break
		}
		w.work <- raw.(string)
	}
	return nil
}

func (w *worker) processLoop(ctx context.Context) error {
	// workItems contains the current set of unprocessed work items.
	// They're each queued by their key.
	workItems := make(map[string]workItem)
	retries := container.NewSet[string]()

	for {
		select {
		case <-ctx.Done():
			// Shut down the queue and drain the work channel.
			w.wq.ShutDown()
			for range w.work {
			}
			close(w.newWorkItems)
			return nil

		case key := <-w.work:
			item, ok := workItems[key]
			if !ok {
				// Since the entry is gone we've already processed it.
				continue
			}

			err := item.process()
			if err != nil {
				if !retries.Contains(key) {
					retries.Add(key)
				}
				w.params.Metrics.TotalRetries.Inc()
				w.wq.AddRateLimited(key)
			} else {
				retries.Delete(key)
				w.params.Metrics.TotalProcessed.Inc()
				w.wq.Forget(key)
				delete(workItems, key)
			}
			w.wq.Done(key)

		case item := <-w.newWorkItems:
			workItems[item.key] = item
			w.wq.Add(item.key)
		}
		w.params.Metrics.Queued.Set(float64(len(workItems)))
		w.params.Metrics.Retried.Set(float64(len(retries)))

		// TODO: Should report status periodically instead. Add a ticker?
		// TODO: Should merge the "Worker" status into the status of the module using the worker.
		//   ... perhaps should be able to grab multiple named reporters per module?
		if len(retries) > 0 {
			w.params.Reporter.Degraded(fmt.Sprintf("%d requests waiting to be retried", len(retries)))
		} else {
			w.params.Reporter.OK(fmt.Sprintf("%.0f requests processed, %d queued", w.params.Metrics.TotalProcessed.Get(), len(workItems)))
		}

	}
}
