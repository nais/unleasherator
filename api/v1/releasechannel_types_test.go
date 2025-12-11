package unleash_nais_io_v1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsCandidate(t *testing.T) {
	testCases := []struct {
		name           string
		releaseChannel *ReleaseChannel
		unleash        *Unleash
		expectedResult bool
	}{
		{
			name: "Matching release channel",
			releaseChannel: &ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-channel",
					Namespace: "test-namespace",
				},
			},
			unleash: &Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unleash",
					Namespace: "test-namespace",
				},
				Spec: UnleashSpec{
					ReleaseChannel: UnleashReleaseChannelConfig{
						Name: "test-channel",
					},
				},
			},
			expectedResult: true,
		},
		{
			name: "Non-matching release channel",
			releaseChannel: &ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-channel",
					Namespace: "test-namespace",
				},
			},
			unleash: &Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unleash",
					Namespace: "test-namespace",
				},
				Spec: UnleashSpec{
					ReleaseChannel: UnleashReleaseChannelConfig{
						Name: "other-channel",
					},
				},
			},
			expectedResult: false,
		},
		// Add more test cases here
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.releaseChannel.IsCandidate(tc.unleash)
			if result != tc.expectedResult {
				t.Errorf("Expected release channel to be a candidate: %v, but got: %v", tc.expectedResult, result)
			}
		})
	}
}
func TestShouldUpdate(t *testing.T) {
	testCases := []struct {
		name           string
		releaseChannel *ReleaseChannel
		unleash        *Unleash
		expectedResult bool
	}{
		{
			name: "Matching release channel with different image",
			releaseChannel: &ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-channel",
					Namespace: "test-namespace",
				},
				Spec: ReleaseChannelSpec{
					Image: "test-image",
				},
			},
			unleash: &Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unleash",
					Namespace: "test-namespace",
				},
				Spec: UnleashSpec{
					ReleaseChannel: UnleashReleaseChannelConfig{
						Name: "test-channel",
					},
					CustomImage: "custom-image",
				},
			},
			expectedResult: true,
		},
		{
			name: "Matching release channel with same image",
			releaseChannel: &ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-channel",
					Namespace: "test-namespace",
				},
				Spec: ReleaseChannelSpec{
					Image: "test-image",
				},
			},
			unleash: &Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unleash",
					Namespace: "test-namespace",
				},
				Spec: UnleashSpec{
					ReleaseChannel: UnleashReleaseChannelConfig{
						Name: "test-channel",
					},
					CustomImage: "test-image",
				},
			},
			expectedResult: false,
		},
		{
			name: "Non-matching release channel",
			releaseChannel: &ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-channel",
					Namespace: "test-namespace",
				},
				Spec: ReleaseChannelSpec{
					Image: "test-image",
				},
			},
			unleash: &Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unleash",
					Namespace: "test-namespace",
				},
				Spec: UnleashSpec{
					ReleaseChannel: UnleashReleaseChannelConfig{
						Name: "other-channel",
					},
					CustomImage: "custom-image",
				},
			},
			expectedResult: false,
		},
		// Add more test cases here
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.releaseChannel.ShouldUpdate(tc.unleash)
			if result != tc.expectedResult {
				t.Errorf("Expected release channel to require update: %v, but got: %v", tc.expectedResult, result)
			}
		})
	}
}
