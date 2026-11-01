package appservice

import "time"

// Use actual processing times, never the scheduled timestamp carried by a
// queued ticker signal. Own I/O time is not evidence of sleep or lost events.
func observationGap(previous, now time.Time, idle bool) bool {
	elapsed := now.Sub(previous)
	wall := now.UTC().Sub(previous.UTC())
	drift := wall - elapsed
	return wall < 0 || drift > 5*time.Second || drift < -5*time.Second || idle && elapsed > 5*time.Second
}

func observationGapLabel(reason string) string {
	switch reason {
	case "watch_setup_partial":
		return "部分媒体目录未成功监听"
	case "observation_pause_or_clock_change", "clock_change_during_update":
		return "观察处理长时间停顿或系统时钟变化"
	case "native_error_or_overflow", "event_queue_overflow", "event_queue_limit", "creation_event_limit":
		return "目录通知异常或事件积压丢失"
	case "raw_event_history_unavailable":
		return "变化事件未能写入私有索引"
	case "source_unavailable":
		return "来源暂不可访问"
	case "incomplete_scan":
		return "部分媒体路径未完成核对"
	default:
		return "未能完整核对媒体变化"
	}
}
