package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDividesCoresAcrossBuilds(t *testing.T) {
	assert.Equal(t, 4, makeJobsPerBuild(16, 4))
	assert.Equal(t, 16, makeJobsPerBuild(16, 1))
	assert.Equal(t, 2, makeJobsPerBuild(8, 3))
}

func TestNeverReturnsZero(t *testing.T) {
	assert.Equal(t, 1, makeJobsPerBuild(2, 8))
	assert.Equal(t, 1, makeJobsPerBuild(1, 4))
	assert.Equal(t, 1, makeJobsPerBuild(0, 0))
}
