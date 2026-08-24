package runtime

import (
	"reflect"
	"testing"
)

func TestCgroupContainerIDs(t *testing.T) {
	id := "7f64c8b9d346718c1d8c11e15963e28cc62803f1979e0690970c9039d4e22144"
	content := "0::/docker/" + id + "\n1:name=systemd:/docker-" + id + ".scope\n"
	want := []string{id, id}
	if got := cgroupContainerIDs(content); !reflect.DeepEqual(got, want) {
		t.Fatalf("cgroupContainerIDs() = %v, want %v", got, want)
	}
}
