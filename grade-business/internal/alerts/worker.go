package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Run 定时从当前本地业务数据生成预警，在关闭应用时退出。
// 参数 ctx 为应用工作任务的生命周期。
// 返回值 无，同步失败时记录错误类型并在下一轮继续。
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 8*time.Second)
		_, err := s.sync(attempt)
		if err == nil {
			_, err = s.sweepRules(attempt)
		}
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("本地业务预警同步失败", "error_type", fmt.Sprintf("%T", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
