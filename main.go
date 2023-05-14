package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/pkg/stdcopy"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

func main() {
	if err := run(); err != nil {
		log.Fatalln(err)
	}
}

func writeFileToTarWriter(tw *tar.Writer, filename string, srcFilename string) error {
	fp, err := os.Open(srcFilename)
	if err != nil {
		return err
	}
	defer fp.Close()

	v := new(strings.Builder)
	if _, err := io.Copy(v, fp); err != nil {
		return err
	}

	fc := v.String()

	header := tar.Header{
		Name: filename,
		Mode: 0777,
		Size: int64(len(fc)),
	}
	if err := tw.WriteHeader(&header); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(fc)); err != nil {
		return err
	}
	return nil
}

func loadFilesRecursive(pathname string, mapRef map[string]string) error {
	dirEntries, err := os.ReadDir(pathname)
	if err != nil {
		return err
	}

	for _, entry := range dirEntries {
		if entry.IsDir() {
			if err := loadFilesRecursive(pathname+"/"+entry.Name(), mapRef); err != nil {
				return err
			}
		} else {
			f, err := os.ReadFile(pathname + "/" + entry.Name())
			if err != nil {
				return err
			}
			mapRef[entry.Name()] = string(f)
		}
	}

	return nil
}

func loadSourceFiles(pathname string) (map[string]string, error) {
	var sourceFiles = make(map[string]string)
	loadFilesRecursive(pathname, sourceFiles)
	return sourceFiles, nil
}

func createTarfileOfCode() (io.Reader, error) {
	sourceFiles, err := loadSourceFiles("examples/python")
	if err != nil {
		return nil, err
	}
	m, err := json.MarshalIndent(sourceFiles, "", "\t")
	if err != nil {
		return nil, err
	}
	fmt.Println(string(m))

	var buffer bytes.Buffer

	tw := tar.NewWriter(&buffer)
	writeFileToTarWriter(tw, "timer.sh", "runner/timer.sh")

	for filePath, fileContents := range sourceFiles {
		header := tar.Header{
			Name: filePath,
			Mode: 0777,
			Size: int64(len(fileContents)),
		}
		if err := tw.WriteHeader(&header); err != nil {
			return nil, err
		}
		if _, err := tw.Write([]byte(fileContents)); err != nil {
			return nil, err
		}
	}

	tw.Close()

	return bytes.NewReader(buffer.Bytes()), nil
}

func disposeContainer(
	ctx context.Context,
	dc *client.Client,
	containerID string,
) {
	if err := dc.ContainerRemove(
		ctx,
		containerID,
		types.ContainerRemoveOptions{},
	); err != nil {
		panic(err)
	}
}

func run() error {
	ctx := context.Background()

	dc, err := client.NewClientWithOpts(
		client.WithAPIVersionNegotiation(),
		client.WithHostFromEnv(),
	)
	if err != nil {
		return err
	}

	// Check if the image runner does not exist.
	filters := filters.NewArgs(
		filters.KeyValuePair{
			Key:   "reference",
			Value: "runner",
		},
	)

	var (
		imageID string
	)

	result, err := dc.ImageList(
		ctx,
		types.ImageListOptions{
			All:     true,
			Filters: filters,
		},
	)
	if err != nil {
		return err
	}

	if len(result) == 0 {
		tarfile, err := archive.TarWithOptions("runner/", &archive.TarOptions{})
		if err != nil {
			return err
		}

		if _, err = dc.ImageBuild(ctx,
			tarfile,
			types.ImageBuildOptions{
				Tags:   []string{"runner:latest"},
				Remove: true,
			},
		); err != nil {
			fmt.Println("error build")
			return err
		}

		r, err := dc.ImageList(
			ctx,
			types.ImageListOptions{
				All:     true,
				Filters: filters,
			},
		)
		if err != nil {
			return err
		}

		imageID = r[0].ID
	} else {
		imageID = result[0].ID
	}

	var (
		memoryLimit = 10_000_000
	)
	createResp, err := dc.ContainerCreate(
		ctx,
		&container.Config{
			Image:           imageID,
			NetworkDisabled: true,
			WorkingDir:      "/code",
			Cmd: []string{
				"sh", "./timer.sh",
			},
		},
		&container.HostConfig{
			Resources: container.Resources{
				Memory:  int64(memoryLimit),
				Devices: nil,
			},
			Privileged: false,
		},
		&network.NetworkingConfig{},
		&v1.Platform{},
		"runner",
	)
	if err != nil {
		return err
	}

	containerID := createResp.ID

	content, err := createTarfileOfCode()
	if err != nil {
		disposeContainer(ctx, dc, containerID)
		return err
	}

	if err := dc.CopyToContainer(
		ctx,
		containerID,
		"/code",
		content,
		types.CopyToContainerOptions{
			AllowOverwriteDirWithFile: true,
		},
	); err != nil {
		disposeContainer(ctx, dc, containerID)
		return err
	}

	if err := dc.ContainerStart(
		ctx,
		containerID,
		types.ContainerStartOptions{},
	); err != nil {
		disposeContainer(ctx, dc, containerID)
		return err
	}

	wr, errCh := dc.ContainerWait(
		ctx,
		containerID,
		container.WaitConditionNotRunning,
	)

	select {
	case c := <-wr:
		if c.Error != nil {
			return err
		}
	case err := <-errCh:
		return err
	}

	f, err := dc.ContainerLogs(
		ctx,
		containerID,
		types.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Details:    true,
		},
	)
	if err != nil {
		return err
	}

	var (
		bufStdout = bytes.NewBuffer(nil)
		bufStderr = bytes.NewBuffer(nil)
	)

	if _, err := stdcopy.StdCopy(bufStdout, bufStderr, f); err != nil {
		return err
	}

	// TODO: Always slice from index 9 upwards to remove SIZE infos.
	// Refer to client.ContainerLogs docs.
	fmt.Println("STDOUT:\n" + bufStdout.String())
	fmt.Println("STDERR:\n" + bufStderr.String())

	disposeContainer(ctx, dc, containerID)
	return nil
}
