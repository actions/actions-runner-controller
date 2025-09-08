package actionssummerwindnet

import (
	"context"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPlanBatchScale(t *testing.T) {
	s := &batchScaler{Log: logr.Discard()}

	var (
		expiry   = 10 * time.Second
		interval = 3 * time.Second

		t0 = time.Now()
		t1 = t0.Add(interval)
		t2 = t1.Add(interval)
	)

	check := func(t *testing.T, amount int, newExpiry time.Duration, wantReservations []v1alpha1.CapacityReservation) {
		t.Helper()

		var (
			op = batchScaleOperation{
				scaleOps: []scaleOperation{
					{
						log: logr.Discard(),
						trigger: v1alpha1.ScaleUpTrigger{
							Amount:   amount,
							Duration: metav1.Duration{Duration: newExpiry},
						},
					},
				},
			}

			hra = &v1alpha1.HorizontalRunnerAutoscaler{
				Spec: v1alpha1.HorizontalRunnerAutoscalerSpec{
					MaxReplicas: intPtr(1),
					ScaleUpTriggers: []v1alpha1.ScaleUpTrigger{
						{
							Amount:   1,
							Duration: metav1.Duration{Duration: newExpiry},
						},
					},
					CapacityReservations: []v1alpha1.CapacityReservation{
						{
							EffectiveTime:  metav1.NewTime(t0),
							ExpirationTime: metav1.NewTime(t0.Add(expiry)),
							Replicas:       1,
						},
						{
							EffectiveTime:  metav1.NewTime(t1),
							ExpirationTime: metav1.NewTime(t1.Add(expiry)),
							Replicas:       1,
						},
					},
				},
			}
		)

		want := hra.DeepCopy()

		want.Spec.CapacityReservations = wantReservations

		got, err := s.planBatchScale(context.Background(), op, hra, t2)

		require.NoError(t, err)
		require.Equal(t, want, got)
	}

	t.Run("scale up", func(t *testing.T) {
		check(t, 1, expiry, []v1alpha1.CapacityReservation{
			{
				// This is kept based on t0 because it falls within maxReplicas
				// i.e. the corresponding runner has assumbed to be already deployed.
				EffectiveTime:  metav1.NewTime(t0),
				ExpirationTime: metav1.NewTime(t0.Add(expiry)),
				Replicas:       1,
			},
			{
				// Updated from t1 to t2 due to this exceeded maxReplicas
				EffectiveTime:  metav1.NewTime(t2),
				ExpirationTime: metav1.NewTime(t2.Add(expiry)),
				Replicas:       1,
			},
			{
				// This is based on t2(=now) because it has been added just now.
				EffectiveTime:  metav1.NewTime(t2),
				ExpirationTime: metav1.NewTime(t2.Add(expiry)),
				Replicas:       1,
			},
		})
	})

	t.Run("scale up reuses previous scale trigger duration for extension", func(t *testing.T) {
		newExpiry := expiry + time.Second
		check(t, 1, newExpiry, []v1alpha1.CapacityReservation{
			{
				// This is kept based on t0 because it falls within maxReplicas
				// i.e. the corresponding runner has assumbed to be already deployed.
				EffectiveTime:  metav1.NewTime(t0),
				ExpirationTime: metav1.NewTime(t0.Add(expiry)),
				Replicas:       1,
			},
			{
				// Updated from t1 to t2 due to this exceeded maxReplicas
				EffectiveTime:  metav1.NewTime(t2),
				ExpirationTime: metav1.NewTime(t2.Add(expiry)),
				Replicas:       1,
			},
			{
				// This is based on t2(=now) because it has been added just now.
				EffectiveTime:  metav1.NewTime(t2),
				ExpirationTime: metav1.NewTime(t2.Add(newExpiry)),
				Replicas:       1,
			},
		})
	})

	t.Run("scale down", func(t *testing.T) {
		check(t, -1, expiry, []v1alpha1.CapacityReservation{
			{
				// Updated from t1 to t2 due to this exceeded maxReplicas
				EffectiveTime:  metav1.NewTime(t2),
				ExpirationTime: metav1.NewTime(t2.Add(expiry)),
				Replicas:       1,
			},
		})
	})

	t.Run("scale down is not affected by new scale trigger duration", func(t *testing.T) {
		check(t, -1, expiry+time.Second, []v1alpha1.CapacityReservation{
			{
				// Updated from t1 to t2 due to this exceeded maxReplicas
				EffectiveTime:  metav1.NewTime(t2),
				ExpirationTime: metav1.NewTime(t2.Add(expiry)),
				Replicas:       1,
			},
		})
	})

	// TODO: Keep refreshing the expiry date even when there are no other scale down/up triggers before the expiration
	t.Run("extension", func(t *testing.T) {
		check(t, 0, expiry, []v1alpha1.CapacityReservation{
			{
				// This is kept based on t0 because it falls within maxReplicas
				// i.e. the corresponding runner has assumbed to be already deployed.
				EffectiveTime:  metav1.NewTime(t0),
				ExpirationTime: metav1.NewTime(t0.Add(expiry)),
				Replicas:       1,
			},
			{
				// Updated from t1 to t2 due to this exceeded maxReplicas
				EffectiveTime:  metav1.NewTime(t2),
				ExpirationTime: metav1.NewTime(t2.Add(expiry)),
				Replicas:       1,
			},
		})
	})
}
