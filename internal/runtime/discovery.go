package runtime

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/bourgils/wakewrap/internal/dockerapi"
)

func discoverPorts(ctx context.Context, ip string, candidates []int, timeout, settle time.Duration, concurrency int, requireAll bool) ([]int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if len(candidates) == 0 {
		candidates = make([]int, 65535)
		for i := range candidates {
			candidates[i] = i + 1
		}
	}
	discovered := make(map[int]struct{})
	var settleDeadline time.Time
	for {
		remaining := make([]int, 0, len(candidates)-len(discovered))
		for _, port := range candidates {
			if _, found := discovered[port]; !found {
				remaining = append(remaining, port)
			}
		}
		open := probePorts(ctx, ip, remaining, concurrency)
		if len(open) > 0 {
			for _, port := range open {
				discovered[port] = struct{}{}
			}
			settleDeadline = time.Now().Add(settle)
		}
		if len(discovered) == len(candidates) || (!requireAll && len(discovered) > 0 && !settleDeadline.IsZero() && !time.Now().Before(settleDeadline)) {
			return sortedPorts(discovered), nil
		}
		select {
		case <-ctx.Done():
			if len(discovered) > 0 && !requireAll {
				return sortedPorts(discovered), nil
			}
			return nil, fmt.Errorf("discover TCP ports on %s: %w", ip, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func probePorts(ctx context.Context, ip string, ports []int, concurrency int) []int {
	if len(ports) == 0 {
		return nil
	}
	if concurrency > len(ports) {
		concurrency = len(ports)
	}
	jobs := make(chan int)
	open := make(chan int, len(ports))
	var workers sync.WaitGroup
	workers.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer workers.Done()
			dialer := net.Dialer{Timeout: 100 * time.Millisecond}
			for port := range jobs {
				connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
				if err == nil {
					_ = connection.Close()
					open <- port
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, port := range ports {
			select {
			case jobs <- port:
			case <-ctx.Done():
				return
			}
		}
	}()
	workers.Wait()
	close(open)
	result := make([]int, 0, len(open))
	for port := range open {
		result = append(result, port)
	}
	return result
}

func sortedPorts(values map[int]struct{}) []int {
	result := make([]int, 0, len(values))
	for port := range values {
		result = append(result, port)
	}
	sort.Ints(result)
	return result
}

func childIP(container dockerapi.ContainerInspect, applicationNetworks []string) (string, error) {
	for _, network := range applicationNetworks {
		endpoint := container.NetworkSettings.Networks[network]
		if endpoint == nil {
			continue
		}
		if endpoint.IPAddress != "" {
			return endpoint.IPAddress, nil
		}
		if endpoint.GlobalIPv6 != "" {
			return endpoint.GlobalIPv6, nil
		}
	}
	return "", fmt.Errorf("child has no IP on application networks %v", applicationNetworks)
}

func address(ip string, port int) string {
	return net.JoinHostPort(ip, strconv.Itoa(port))
}
