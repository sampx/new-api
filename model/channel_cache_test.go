package model

import (
	"testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/QuantumNous/new-api/common"
)

func TestGetRandomSatisfiedChannel_Exclude(t *testing.T) {
	// Simple mock setup for testing channel selection logic
	// In actual implementation we need to mock channelsIDM and group2model2channels
	common.MemoryCacheEnabled = true
	
	// Mock channels
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Type: 1, Key: "k1", ChannelInfo: ChannelInfo{IsMultiKey: false}}, // Priority 8
		2: {Id: 2, Type: 1, Key: "k2\nk3", ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeySize: 2}}, // Priority 8
		3: {Id: 3, Type: 1, Key: "k4", ChannelInfo: ChannelInfo{IsMultiKey: false}}, // Priority 7
	}
	
	// mock Priority and weight getters via struct values if possible, 
	// but those are DB models so we need to inject fields.
	channelsIDM[1].Priority = common.GetPointer(int64(8))
	channelsIDM[1].Weight = common.GetPointer(uint(10))
	channelsIDM[2].Priority = common.GetPointer(int64(8))
	channelsIDM[2].Weight = common.GetPointer(uint(10))
	channelsIDM[3].Priority = common.GetPointer(int64(7))
	channelsIDM[3].Weight = common.GetPointer(uint(10))

	group2model2channels = map[string]map[string][]int{
		"default": {
			"gpt-3": {1, 2, 3},
		},
	}

	t.Run("first pick priority 8", func(t *testing.T) {
		ch, isExhausted, err := GetRandomSatisfiedChannel("default", "gpt-3", "", nil)
		require.NoError(t, err)
		assert.False(t, isExhausted)
		assert.Contains(t, []int{1, 2}, ch.Id)
	})

	t.Run("exclude channel 1, should pick channel 2", func(t *testing.T) {
		exclude := map[string]bool{"1_0": true}
		ch, isExhausted, err := GetRandomSatisfiedChannel("default", "gpt-3", "", exclude)
		require.NoError(t, err)
		assert.False(t, isExhausted)
		assert.Equal(t, 2, ch.Id)
	})

	t.Run("exclude channel 1 and partial channel 2, should pick channel 2", func(t *testing.T) {
		exclude := map[string]bool{"1_0": true, "2_0": true}
		ch, isExhausted, err := GetRandomSatisfiedChannel("default", "gpt-3", "", exclude)
		require.NoError(t, err)
		assert.False(t, isExhausted)
		assert.Equal(t, 2, ch.Id)
	})

	t.Run("exclude all in priority 8, should fallback to priority 7", func(t *testing.T) {
		exclude := map[string]bool{"1_0": true, "2_0": true, "2_1": true}
		ch, isExhausted, err := GetRandomSatisfiedChannel("default", "gpt-3", "", exclude)
		require.NoError(t, err)
		assert.False(t, isExhausted)
		assert.Equal(t, 3, ch.Id)
	})

	t.Run("exclude all", func(t *testing.T) {
		exclude := map[string]bool{"1_0": true, "2_0": true, "2_1": true, "3_0": true}
		ch, isExhausted, err := GetRandomSatisfiedChannel("default", "gpt-3", "", exclude)
		require.Error(t, err)
		assert.True(t, isExhausted)
		assert.Nil(t, ch)
	})
}
