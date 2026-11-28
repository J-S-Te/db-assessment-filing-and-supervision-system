interface SceneElements {
  canvas: HTMLCanvasElement
  layer: HTMLDivElement
}

export interface SignInSceneController {
  setResolution(scale: number): void
  dispose(): void
}

/**
 * 用途 挂载登录页三维场景。
 * 参数 elements 为挂载节点，signal 为取消信号。
 * onResize、onLost、onFirstFrame 分别通知尺寸、上下文丢失和首帧完成。
 * 返回值 场景控制器，初始化失败时拒绝。
 */
export function mountScene(
  elements: SceneElements,
  signal: AbortSignal,
  onResize: (scale: number, width: number, height: number) => void,
  onLost: () => void,
  onFirstFrame: () => void,
): Promise<SignInSceneController>
