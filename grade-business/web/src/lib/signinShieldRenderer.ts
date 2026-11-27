import { shieldCoreOffset, shieldMesh, shieldSolidVertices } from './signinShieldMesh'

const vertexShader = `
attribute vec3 a_position;
attribute vec2 a_uv;
attribute vec4 a_style;
uniform vec2 u_angles;
uniform vec3 u_camera;
uniform mediump float u_impact;
varying mediump vec2 v_uv;
varying mediump vec4 v_style;

void main() {
  vec3 point = a_position;
  if (a_style.w > 2.5) {
    vec2 center = vec2(0.0, ${shieldCoreOffset.toFixed(6)});
    float scale = mix(0.65, 3.15, 1.0 - pow(1.0 - u_impact, 3.0));
    point.xy = center + (point.xy - center) * scale;
  }

  point.x *= 0.93;
  vec2 pitch = vec2(cos(u_angles.x), sin(u_angles.x));
  vec2 yaw = vec2(cos(u_angles.y), sin(u_angles.y));
  point.xz = mat2(yaw.x, -yaw.y, yaw.y, yaw.x) * point.xz;
  point.yz = mat2(pitch.x, -pitch.y, pitch.y, pitch.x) * point.yz;
  gl_Position = vec4(point.xy * u_camera.xy, -point.z, 1.0 - point.z * u_camera.z);
  v_uv = a_uv;
  v_style = a_style;
}
`

const fragmentShader = `
precision mediump float;
uniform sampler2D u_texture;
uniform mediump float u_impact;
varying mediump vec2 v_uv;
varying mediump vec4 v_style;

void main() {
  if (v_style.w < 0.5) {
    gl_FragColor = vec4(v_style.rgb, 1.0);
  } else if (v_style.w < 1.5) {
    vec4 color = texture2D(u_texture, v_uv);
    if (color.a < 0.18) discard;
    gl_FragColor = vec4(color.rgb * v_style.rgb, color.a);
  } else {
    vec2 delta = v_uv * 2.0 - 1.0;
    float radius = length(delta);
    if (v_style.w < 2.5) {
      float line = smoothstep(0.90, 0.96, radius) * (1.0 - smoothstep(0.98, 1.0, radius));
      vec3 light = delta.x > 0.0 && abs(delta.y) < 0.67
        ? vec3(1.0, 0.36, 0.50) : vec3(0.40, 0.80, 1.0);
      gl_FragColor = vec4(light, line * 0.55);
    } else {
      if (u_impact >= 1.0) discard;
      float line = smoothstep(0.87, 0.94, radius) * (1.0 - smoothstep(0.95, 1.0, radius));
      float fill = max(0.0, 0.65 - radius) * 0.40;
      gl_FragColor = vec4(1.0, 0.15, 0.29, (line + fill) * (1.0 - u_impact));
    }
  }
}
`

interface RenderFrame {
  width: number
  height: number
  modelWidth: number
  pitch: number
  yaw: number
  impact: number
}

const maxCanvasPixels = 540_000
const maxPixelRatio = 1.75

/**
 * 用途 构建 WebGL 着色器，在编译失败时释放对象。
 * 参数 gl 是当前绘图上下文，type 和 source 指定着色器类型与源码。
 * 返回值 已编译着色器，编译失败时抛出错误。
 */
function compile(gl: WebGLRenderingContext, type: number, source: string) {
  const shader = gl.createShader(type)
  if (!shader) throw new Error('无法分配盾牌着色器')
  gl.shaderSource(shader, source)
  gl.compileShader(shader)
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    const message = gl.getShaderInfoLog(shader)
    gl.deleteShader(shader)
    throw new Error(message || '盾牌着色器编译失败')
  }
  return shader
}

/**
 * 用途 创建并链接盾牌的单一渲染程序。
 * 参数 gl 为当前盾牌的 WebGL 上下文。
 * 返回值 已链接的程序，链接失败时抛出错误。
 */
function link(gl: WebGLRenderingContext) {
  const vertex = compile(gl, gl.VERTEX_SHADER, vertexShader)
  let fragment: WebGLShader | undefined
  let program: WebGLProgram | null = null
  try {
    fragment = compile(gl, gl.FRAGMENT_SHADER, fragmentShader)
    program = gl.createProgram()
    if (!program) throw new Error('无法分配盾牌绘图程序')
    gl.attachShader(program, vertex)
    gl.attachShader(program, fragment)
    gl.linkProgram(program)
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
      throw new Error(gl.getProgramInfoLog(program) || '盾牌绘图程序链接失败')
    }
    return program
  } catch (error) {
    if (program) gl.deleteProgram(program)
    throw error
  } finally {
    gl.deleteShader(vertex)
    if (fragment) gl.deleteShader(fragment)
  }
}

/**
 * 用途 将共享几何和盾面纹理一次性上传，以深度缓冲统一绘制整面盾牌。
 * 参数 canvas 为组件内画布，image 为已翻转坐标的紧凑纹理位图。
 * 返回值 绘制与释放两个操作，不支持 WebGL 时返回 null。
 */
export function createShieldRenderer(canvas: HTMLCanvasElement, image: ImageBitmap) {
  const gl = canvas.getContext('webgl', {
    alpha: true, antialias: true, depth: true, premultipliedAlpha: false,
  })
  if (!gl) return null
  const program = link(gl)
  const mesh = gl.createBuffer()
  const texture = gl.createTexture()
  if (!mesh || !texture) {
    gl.deleteProgram(program)
    gl.deleteBuffer(mesh)
    gl.deleteTexture(texture)
    return null
  }

  gl.useProgram(program)
  gl.bindBuffer(gl.ARRAY_BUFFER, mesh)
  gl.bufferData(gl.ARRAY_BUFFER, shieldMesh, gl.STATIC_DRAW)
  for (const [name, size, offset] of [
    ['a_position', 3, 0], ['a_uv', 2, 12], ['a_style', 4, 20],
  ] as const) {
    const location = gl.getAttribLocation(program, name)
    gl.enableVertexAttribArray(location)
    gl.vertexAttribPointer(location, size, gl.FLOAT, false, 36, offset)
  }
  gl.bindTexture(gl.TEXTURE_2D, texture)
  gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image)
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
  gl.uniform1i(gl.getUniformLocation(program, 'u_texture'), 0)
  const camera = gl.getUniformLocation(program, 'u_camera')
  const angles = gl.getUniformLocation(program, 'u_angles')
  const impact = gl.getUniformLocation(program, 'u_impact')
  gl.enable(gl.DEPTH_TEST)
  gl.blendFuncSeparate(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA, gl.ONE, gl.ONE_MINUS_SRC_ALPHA)

  return {
    /**
     * 用途 先绘制盾牌实体，再按深度叠加核心光环和冲击波。
     * 参数 frame 为布局尺寸、姿态以及当前时刻的冲击波状态。
     * 返回值 无。
     */
    draw(frame: RenderFrame) {
      const ratio = Math.min(window.devicePixelRatio, maxPixelRatio,
        Math.sqrt(maxCanvasPixels / (frame.width * frame.height)))
      const width = Math.max(1, Math.floor(frame.width * ratio))
      const height = Math.max(1, Math.floor(frame.height * ratio))
      if (canvas.width !== width || canvas.height !== height) {
        canvas.width = width
        canvas.height = height
        gl.viewport(0, 0, width, height)
      }
      gl.uniform2f(angles, frame.pitch * Math.PI / 180, frame.yaw * Math.PI / 180)
      gl.uniform3f(camera, 2 * frame.modelWidth / frame.width,
        2 * frame.modelWidth / frame.height, frame.modelWidth / 1000)
      gl.uniform1f(impact, frame.impact)
      gl.depthMask(true)
      gl.disable(gl.BLEND)
      gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
      gl.drawArrays(gl.TRIANGLES, 0, shieldSolidVertices)
      gl.depthMask(false)
      gl.enable(gl.BLEND)
      gl.drawArrays(gl.TRIANGLES, shieldSolidVertices, shieldMesh.length / 9 - shieldSolidVertices)
    },

    /**
     * 用途 在卸载或重建时释放盾牌占用的 GPU 资源。
     * 参数 无。
     * 返回值 无。
     */
    dispose() {
      gl.deleteBuffer(mesh)
      gl.deleteTexture(texture)
      gl.deleteProgram(program)
      canvas.width = canvas.height = 1
    },
  }
}
