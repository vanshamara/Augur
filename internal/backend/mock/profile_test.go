package mock

import (
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/core"
)

func TestRisingP99LatencyClimbs(t *testing.T) {
	p := RisingP99()
	early := p.ParamsAt(0).MeanLatencyMs
	later := p.ParamsAt(10 * time.Minute).MeanLatencyMs
	if later <= early {
		t.Fatalf("rising p99 should get slower over time, got %v then %v", early, later)
	}
}

func TestColdStartLatencyFalls(t *testing.T) {
	p := ColdStart()
	atStart := p.ParamsAt(0).MeanLatencyMs
	warmed := p.ParamsAt(60 * time.Second).MeanLatencyMs
	if warmed >= atStart {
		t.Fatalf("cold start should speed up over time, got %v then %v", atStart, warmed)
	}
}

func TestIntermittent500sSpikesThenRecovers(t *testing.T) {
	p := Intermittent500s()
	duringSpike := p.ParamsAt(3 * time.Second).ErrorRate
	afterSpike := p.ParamsAt(30 * time.Second).ErrorRate
	if duringSpike <= afterSpike {
		t.Fatalf("error rate should be higher during the spike window, got %v then %v", duringSpike, afterSpike)
	}
}

func TestSteadyProfilesDoNotDrift(t *testing.T) {
	p := SlowStable()
	first := p.ParamsAt(0)
	later := p.ParamsAt(time.Hour)
	if first != later {
		t.Fatal("a steady profile should return the same params at any time")
	}
}

func TestRequestShapeProfilesChangeByTaskType(t *testing.T) {
	cheap := CheapChatSpecialist()
	strong := StrongReasoningSpecialist()
	chat := core.Request{Features: core.Features{Type: core.Chat}}
	reasoning := core.Request{Features: core.Features{Type: core.Reasoning}}

	if cheap.ParamsFor(0, chat).MeanLatencyMs >= cheap.ParamsFor(0, reasoning).MeanLatencyMs {
		t.Fatal("cheap specialist should be faster for chat than reasoning")
	}
	if strong.ParamsFor(0, reasoning).MeanLatencyMs >= strong.ParamsFor(0, chat).MeanLatencyMs {
		t.Fatal("strong specialist should be faster for reasoning than chat")
	}
}
