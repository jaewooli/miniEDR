package miniedr

import "github.com/jaewooli/miniedr/capturer"

// AlertPipeline glues detection and response together.
// It evaluates rules on an InfoData and then runs responses with optional policy.
type AlertPipeline struct {
	Detector  *Detector
	Router    *ResponseRouter
	Responder *ResponderPipeline // used when Router is nil
}

// Process runs detection then responses; it returns alerts and any response errors.
func (p *AlertPipeline) Process(info capturer.InfoData) ([]Alert, []error) {
	if p == nil || p.Detector == nil {
		return nil, nil
	}
	alerts := p.Detector.Evaluate(info)
	if len(alerts) == 0 {
		return alerts, nil
	}

	if p.Router != nil {
		errs := p.Router.Run(alerts)
		return alerts, errs
	}
	if p.Responder != nil {
		errs := p.Responder.Run(alerts)
		return alerts, errs
	}
	// no responders configured; return alerts for callers to inspect
	return alerts, nil
}
