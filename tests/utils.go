package dockere2e

import (
	"context"
	// "strings"
	"time"

	"github.com/pkg/errors"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
)

const E2EServiceLabel = "e2etesting"

func GetClient() (*client.Client, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}
	return cli, nil
}

// CleanTestServices removes all e2etesting services with the specified labels
func CleanTestServices(ctx context.Context, cli *client.Client, labels ...string) error {
	// create a new filter for our test label
	f := GetTestFilter(labels...)
	opts := types.ServiceListOptions{
		Filters: f,
	}
	// get the services with that label
	services, err := cli.ServiceList(ctx, opts)
	if err != nil {
		return err
	}

	// delete all of them
	for _, service := range services {
		cli.ServiceRemove(ctx, service.ID)
	}

	return nil
}

// CannedServiceSpec returns a ready-to-go service spec with name and replicas
func CannedServiceSpec(name string, replicas uint64, labels ...string) swarm.ServiceSpec {
	// first create the canned spec
	spec := swarm.ServiceSpec{
		Annotations: swarm.Annotations{
			Name:   name,
			Labels: map[string]string{E2EServiceLabel: "true"},
		},
		TaskTemplate: swarm.TaskSpec{
			ContainerSpec: swarm.ContainerSpec{
				Image: "nginx",
			},
		},
		Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}},
	}

	// then, add labels
	for _, label := range labels {
		spec.Annotations.Labels[label] = ""
	}

	return spec
}

// WaitForConverge does test every poll
// returns nothing if test returns nothing, or test's error after context is done
//
// make sure that context is either canceled or given a timeout; if it isn't,
// test will run until half life 3 is released.
//
// if an irrecoverable error is noticed during the test function, calling the
// context's cancel func from inside the test can be used to abort the test
// before the timeout
func WaitForConverge(ctx context.Context, poll time.Duration, test func() error) error {
	// create a ticker and a timer
	r := time.NewTicker(poll)
	// don't forget to close this thing
	// do we have to close this thing? idk
	defer r.Stop()

	var err error
	for {
		select {
		case <-ctx.Done():
			// if the context is done, just return whatever our last saved error was
			return errors.Wrap(err, "failed to converge")
		case <-r.C:
			// do test, save the error
			err = test()
			// TODO(dperny) ughhhhhhhhhhhhhhhhhhhhh
			// if the context times out during a call to the docker api, we
			// will get context deadline exceeded which could mask the real
			// error. in this case, if we already have an error, discard the
			// the deadline exceeded error
			/*
				if err == nil ||
					terr == nil ||
					(terr != nil && !strings.Contains(terr.Error(), "context deadline exceeded")) {
					err = terr
				}
			*/
		}
		// if there is no error, we're done
		if err == nil {
			return nil
		}
	}
}

// GetServiceTasks returns all of the tasks associated with a the service
func GetServiceTasks(ctx context.Context, cli *client.Client, serviceID string) ([]swarm.Task, error) {
	// get the default filter
	filterArgs := GetTestFilter()
	// all of the tasks that we want to be running
	filterArgs.Add("desired-state", "running")
	filterArgs.Add("desired-state", "ready")
	// on the service we're requesting
	filterArgs.Add("service", serviceID)
	return cli.TaskList(ctx, types.TaskListOptions{Filters: filterArgs})
}

// GetTestFilter creates a default filter for labels
// Always adds the E2EServiceLabel, plus some user-defined labels.
// if you need more fitlers, add them to the returned value.
func GetTestFilter(labels ...string) filters.Args {
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", E2EServiceLabel)
	for _, l := range labels {
		filterArgs.Add("label", l)
	}
	return filterArgs
}

// ScaleCheck returns a generator for scale checking functions. Pass in the
// service and client once to get the scale checker generator. Pass the context
// and replicas to that to get a scale checker
func ScaleCheck(serviceID string, cli *client.Client) func(context.Context, int) func() error {
	return func(ctx context.Context, replicas int) func() error {
		return func() error {
			// get all of the tasks for the service
			tasks, err := GetServiceTasks(ctx, cli, serviceID)
			if err != nil {
				return errors.Wrap(err, "failed to get service tasks")
			}
			// check for correct number of tasks
			if t := len(tasks); t != replicas {
				return errors.Errorf("wrong number of tasks, got %v expected %v", t, replicas)
			}
			// verify that all tasks are in the RUNNING state
			for _, task := range tasks {
				if task.Status.State != swarm.TaskStateRunning {
					return errors.New("a task is not yet running")
				}
			}
			// if all of the above checks out, service has converged
			return nil
		}
	}
}

// GetNodeIps returns a list of all node IP addresses in the cluster
func GetNodeIps(cli *client.Client) ([]string, error) {
	nodes, err := cli.NodeList(context.TODO(), types.NodeListOptions{})
	if err != nil {
		return nil, err
	}
	// standard cluster is like 3 managers 5 workers, so 8 is a good start
	ips := make([]string, 0, 8)
	for _, node := range nodes {
		ip := node.Status.Addr
		if ip == "" {
			return nil, errors.New("some node didn't have an associated IP")
		}
		ips = append(ips, ip)
	}
	return ips, nil
}
