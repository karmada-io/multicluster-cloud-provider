GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

# Images management
REGISTRY?="docker.io/karmada"

TARGETS := multicluster-provider-fake

# Build code.
#
# Args:
#   GOOS:   OS to build.
#   GOARCH: Arch to build.
#
# Example:
#   make
#   make all
#   make multicluster-provider-fake
#   make multicluster-provider-fake GOOS=linux
CMD_TARGET=$(TARGETS)

.PHONY: all
all: $(CMD_TARGET)

.PHONY: $(CMD_TARGET)
$(CMD_TARGET):
	BUILD_PLATFORMS=$(GOOS)/$(GOARCH) hack/build.sh $@

# Build image.
#
# Args:
#   GOARCH:      Arch to build.
#   OUTPUT_TYPE: Destination to save image(docker/registry).
#
# Example:
#   make images
#   make image-multicluster-provider-fake
#   make image-multicluster-provider-fake GOARCH=arm64
IMAGE_TARGET=$(addprefix image-, $(TARGETS))
.PHONY: $(IMAGE_TARGET)
$(IMAGE_TARGET):
	set -e;\
	target=$$(echo $(subst image-,,$@));\
	make $$target GOOS=linux;\
	VERSION=$(VERSION) REGISTRY=$(REGISTRY) BUILD_PLATFORMS=linux/$(GOARCH) hack/docker.sh $$target

images: $(IMAGE_TARGET)

.PHONY: clean
clean:
	rm -rf _tmp _output

.PHONY: verify
verify:
	hack/verify-all.sh
