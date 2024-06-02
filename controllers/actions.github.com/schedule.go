package actionsgithubcom

import (
	"fmt"
	"time"

	"github.com/teambition/rrule-go"
)

type RecurrenceRule struct {
	Frequency string
	UntilTime time.Time
}

type Period struct {
	StartTime time.Time
	EndTime   time.Time
}

func (r *Period) String() string {
	if r == nil {
		return ""
	}

	return r.StartTime.Format(time.RFC3339) + "-" + r.EndTime.Format(time.RFC3339)
}

func MatchSchedule(now time.Time, startTime, endTime time.Time, recurrenceRule RecurrenceRule) (*Period, *Period, error) {
	return calculateActiveAndUpcomingRecurringPeriods(
		now,
		startTime,
		endTime,
		recurrenceRule.Frequency,
		recurrenceRule.UntilTime,
	)
}

func calculateActiveAndUpcomingRecurringPeriods(now, startTime, endTime time.Time, frequency string, untilTime time.Time) (*Period, *Period, error) {
	var freqValue rrule.Frequency

	var freqDurationDay int
	var freqDurationMonth int
	var freqDurationYear int

	switch frequency {
	case "Daily":
		freqValue = rrule.DAILY
		freqDurationDay = 1
	case "Weekly":
		freqValue = rrule.WEEKLY
		freqDurationDay = 7
	case "Monthly":
		freqValue = rrule.MONTHLY
		freqDurationMonth = 1
	case "Yearly":
		freqValue = rrule.YEARLY
		freqDurationYear = 1
	case "":
		if now.Before(startTime) {
			return nil, &Period{StartTime: startTime, EndTime: endTime}, nil
		}

		if now.Before(endTime) {
			return &Period{StartTime: startTime, EndTime: endTime}, nil, nil
		}

		return nil, nil, nil
	default:
		return nil, nil, fmt.Errorf(`invalid freq %q: It must be one of "Daily", "Weekly", "Monthly", and "Yearly"`, frequency)
	}

	freqDurationLater := time.Date(
		now.Year()+freqDurationYear,
		time.Month(int(now.Month())+freqDurationMonth),
		now.Day()+freqDurationDay,
		now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), now.Location(),
	)

	freqDuration := freqDurationLater.Sub(now)

	overrideDuration := endTime.Sub(startTime)
	if overrideDuration > freqDuration {
		return nil, nil, fmt.Errorf("override's duration %s must be equal to or shorter than the duration implied by freq %q (%s)", overrideDuration, frequency, freqDuration)
	}

	rrule, err := rrule.NewRRule(rrule.ROption{
		Freq:    freqValue,
		Dtstart: startTime,
		Until:   untilTime,
	})
	if err != nil {
		return nil, nil, err
	}

	overrideDurationBefore := now.Add(-overrideDuration + 1)
	activeOverrideStarts := rrule.Between(overrideDurationBefore, now, true)

	var active *Period

	if len(activeOverrideStarts) > 1 {
		return nil, nil, fmt.Errorf("[bug] unexpted number of active overrides found: %v", activeOverrideStarts)
	} else if len(activeOverrideStarts) == 1 {
		active = &Period{
			StartTime: activeOverrideStarts[0],
			EndTime:   activeOverrideStarts[0].Add(overrideDuration),
		}
	}

	oneSecondLater := now.Add(1)
	upcomingOverrideStarts := rrule.Between(oneSecondLater, freqDurationLater, true)

	var next *Period

	if len(upcomingOverrideStarts) > 0 {
		next = &Period{
			StartTime: upcomingOverrideStarts[0],
			EndTime:   upcomingOverrideStarts[0].Add(overrideDuration),
		}
	}

	return active, next, nil
}
