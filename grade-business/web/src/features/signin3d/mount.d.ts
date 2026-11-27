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
 * 参数 elements 为挂载节点，signal 为取消信号，onResize、onLost 为场景状态回调。
 * 返回值 场景控制器，初始化失败时拒绝。
 */
export function mountScene(
  elements: SceneElements,
  signal: AbortSignal,
  onResize: (scale: number, width: number, height: number) => void,
  onLost: () => void,
): Promise<SignInSceneController>
