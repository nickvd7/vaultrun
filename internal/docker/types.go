package docker

import "github.com/docker/docker/api/types/image"

func dockerImagePullOptions() image.PullOptions {
	return image.PullOptions{}
}
