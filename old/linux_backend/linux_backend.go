package linux_backend

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"sync"
	"time"

	"github.com/cloudfoundry-incubator/garden"
	"github.com/cloudfoundry-incubator/garden-linux/old/system_info"
	"github.com/pivotal-golang/lager"
)

type Container interface {
	ID() string
	Properties() garden.Properties
	GraceTime() time.Duration

	Start() error

	Snapshot(io.Writer) error
	Cleanup()

	garden.Container
}

type ContainerPool interface {
	Setup() error
	Create(garden.ContainerSpec) (Container, error)
	Restore(io.Reader) (Container, error)
	Destroy(Container) error
	Prune(keep map[string]bool) error
	MaxContainers() int
}

type LinuxBackend struct {
	logger lager.Logger

	containerPool ContainerPool
	systemInfo    system_info.Provider
	snapshotsPath string

	containers      map[string]Container
	containersMutex *sync.RWMutex
}

type HandleExistsError struct {
	Handle string
}

func (e HandleExistsError) Error() string {
	return fmt.Sprintf("handle already exists: %s", e.Handle)
}

type FailedToSnapshotError struct {
	OriginalError error
}

func (e FailedToSnapshotError) Error() string {
	return fmt.Sprintf("failed to save snapshot: %s", e.OriginalError)
}

func New(logger lager.Logger, containerPool ContainerPool, systemInfo system_info.Provider, snapshotsPath string) *LinuxBackend {
	return &LinuxBackend{
		logger: logger.Session("backend"),

		containerPool: containerPool,
		systemInfo:    systemInfo,
		snapshotsPath: snapshotsPath,

		containers:      make(map[string]Container),
		containersMutex: new(sync.RWMutex),
	}
}

func (b *LinuxBackend) Setup() error {
	return b.containerPool.Setup()
}

func (b *LinuxBackend) Start() error {
	if b.snapshotsPath != "" {
		_, err := os.Stat(b.snapshotsPath)
		if err == nil {
			b.restoreSnapshots()
			os.RemoveAll(b.snapshotsPath)
		}

		err = os.MkdirAll(b.snapshotsPath, 0755)
		if err != nil {
			return err
		}
	}

	keep := map[string]bool{}

	b.containersMutex.RLock()
	containers := b.containers
	b.containersMutex.RUnlock()

	for _, container := range containers {
		keep[container.ID()] = true
	}

	return b.containerPool.Prune(keep)
}

func (b *LinuxBackend) Ping() error {
	return nil
}

func (b *LinuxBackend) Capacity() (garden.Capacity, error) {
	totalMemory, err := b.systemInfo.TotalMemory()
	if err != nil {
		return garden.Capacity{}, err
	}

	totalDisk, err := b.systemInfo.TotalDisk()
	if err != nil {
		return garden.Capacity{}, err
	}

	return garden.Capacity{
		MemoryInBytes: totalMemory,
		DiskInBytes:   totalDisk,
		MaxContainers: uint64(b.containerPool.MaxContainers()),
	}, nil
}

func (b *LinuxBackend) Create(spec garden.ContainerSpec) (garden.Container, error) {
	if spec.Handle != "" {
		b.containersMutex.RLock()
		_, exists := b.containers[spec.Handle]
		b.containersMutex.RUnlock()

		if exists {
			return nil, HandleExistsError{Handle: spec.Handle}
		}
	}

	container, err := b.containerPool.Create(spec)
	if err != nil {
		return nil, err
	}

	err = container.Start()
	if err != nil {
		return nil, err
	}

	b.containersMutex.Lock()
	b.containers[container.Handle()] = container
	b.containersMutex.Unlock()

	return container, nil
}

func (b *LinuxBackend) Destroy(handle string) error {
	b.containersMutex.RLock()
	container, found := b.containers[handle]
	b.containersMutex.RUnlock()

	if !found {
		return garden.ContainerNotFoundError{handle}
	}

	err := b.containerPool.Destroy(container)
	if err != nil {
		return err
	}

	b.containersMutex.Lock()
	delete(b.containers, container.Handle())
	b.containersMutex.Unlock()

	return nil
}

func (b *LinuxBackend) Containers(filter garden.Properties) (containers []garden.Container, err error) {
	b.containersMutex.RLock()
	defer b.containersMutex.RUnlock()

	for _, container := range b.containers {
		if containerHasProperties(container, filter) {
			containers = append(containers, container)
		}
	}

	return containers, nil
}

func (b *LinuxBackend) Lookup(handle string) (garden.Container, error) {
	b.containersMutex.RLock()
	defer b.containersMutex.RUnlock()

	container, found := b.containers[handle]
	if !found {
		return nil, garden.ContainerNotFoundError{handle}
	}

	return container, nil
}

func (b *LinuxBackend) GraceTime(container garden.Container) time.Duration {
	return container.(Container).GraceTime()
}

func (b *LinuxBackend) Stop() {
	b.containersMutex.RLock()
	defer b.containersMutex.RUnlock()

	for _, container := range b.containers {
		container.Cleanup()
		err := b.saveSnapshot(container)
		if err != nil {
			b.logger.Error("failed-to-save-snapshot", err, lager.Data{
				"container": container.ID(),
			})
		}
	}
}

func (b *LinuxBackend) restoreSnapshots() {
	sLog := b.logger.Session("restore")

	entries, err := ioutil.ReadDir(b.snapshotsPath)
	if err != nil {
		b.logger.Error("failed-to-read-snapshots", err, lager.Data{
			"from": b.snapshotsPath,
		})
	}

	for _, entry := range entries {
		snapshot := path.Join(b.snapshotsPath, entry.Name())

		lLog := sLog.Session("load", lager.Data{
			"snapshot": entry.Name(),
		})

		lLog.Debug("loading")

		file, err := os.Open(snapshot)
		if err != nil {
			lLog.Error("failed-to-open", err)
		}

		_, err = b.restore(file)
		if err != nil {
			lLog.Error("failed-to-restore", err)
		}
	}
}

func (b *LinuxBackend) saveSnapshot(container Container) error {
	if b.snapshotsPath == "" {
		return nil
	}

	b.logger.Info("save-snapshot", lager.Data{
		"container": container.ID(),
	})

	snapshotPath := path.Join(b.snapshotsPath, container.ID())

	snapshot, err := os.Create(snapshotPath)
	if err != nil {
		return &FailedToSnapshotError{err}
	}

	err = container.Snapshot(snapshot)
	if err != nil {
		return &FailedToSnapshotError{err}
	}

	return snapshot.Close()
}

func (b *LinuxBackend) restore(snapshot io.Reader) (garden.Container, error) {
	container, err := b.containerPool.Restore(snapshot)
	if err != nil {
		return nil, err
	}

	b.containersMutex.Lock()
	b.containers[container.Handle()] = container
	b.containersMutex.Unlock()

	return container, nil
}

func containerHasProperties(container Container, properties garden.Properties) bool {
	containerProps := container.Properties()

	for key, val := range properties {
		cval, ok := containerProps[key]
		if !ok {
			return false
		}

		if cval != val {
			return false
		}
	}

	return true
}
