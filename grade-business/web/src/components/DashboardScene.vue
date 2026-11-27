<script setup lang="ts">
import { onMounted, ref } from 'vue'

/**
 * 驾驶舱的三维空间背景层。
 *
 * 只绘制背景元素，不承载任何文字与数据，全部内容层位于其上方，
 * 保证正文始终不被遮挡。系统开启减少动态效果时停止全部动效。
 */
const space = ref('')
const particles = ref<{ left: string; top: string; size: number; delay: number; duration: number }[]>([])

onMounted(() => {
  const asset = import.meta.glob<string>('../assets/dashboard-space.webp', {
    eager: true, import: 'default', query: '?url&no-inline',
  })
  space.value = asset['../assets/dashboard-space.webp'] ?? ''
  particles.value = Array.from({ length: 28 }, (_, index) => ({
    left: `${(index * 37) % 100}%`,
    top: `${(index * 61) % 100}%`,
    size: 1.5 + (index % 4) * 1.1,
    delay: -((index % 7) * 1.7),
    duration: 9 + (index % 5) * 2,
  }))
})
</script>

<template>
  <div class="dashboard-scene" aria-hidden="true">
    <div class="scene-space" :style="space ? { backgroundImage: `url(${space})` } : undefined" />
    <div class="scene-dots" />
    <div class="scene-flow">
      <i /><i /><i />
      <b /><b /><b />
    </div>
    <div class="scene-floor"><div class="scene-floor-grid" /></div>
    <div class="scene-horizon-glow" />
    <div class="scene-horizon" />
    <div class="scene-fog" />
    <div class="scene-spot scene-spot-left" />
    <div class="scene-spot scene-spot-center" />
    <div class="scene-spot scene-spot-right" />
    <div class="scene-particles">
      <i v-for="(item, index) in particles" :key="index"
        :style="{ left: item.left, top: item.top, width: `${item.size}px`, height: `${item.size}px`,
          animationDelay: `${item.delay}s`, animationDuration: `${item.duration}s` }" />
    </div>
    <div class="scene-vignette" />
  </div>
</template>

<style scoped>
.dashboard-scene{position:fixed;inset:0;z-index:0;pointer-events:none;overflow:hidden;perspective:1400px;perspective-origin:50% 44%}
.scene-space{position:absolute;inset:0;background-color:#03060e;background-position:50% 100%;background-size:cover;background-repeat:no-repeat;opacity:.62;
  -webkit-mask-image:linear-gradient(180deg,transparent 0,#000 42%,#000 100%);
  mask-image:linear-gradient(180deg,transparent 0,#000 42%,#000 100%)}
.scene-dots{position:absolute;inset:0;opacity:.5;
  background:radial-gradient(circle,rgba(95,216,255,.26) 1px,transparent 1.6px) 0 0/26px 26px;
  -webkit-mask-image:radial-gradient(120% 80% at 50% 40%,#000 18%,transparent 78%);
  mask-image:radial-gradient(120% 80% at 50% 40%,#000 18%,transparent 78%)}
.scene-flow{position:absolute;inset:0;overflow:hidden;
  -webkit-mask-image:linear-gradient(180deg,transparent,#000 22%,#000 74%,transparent),linear-gradient(90deg,transparent,#000 16% 84%,transparent);
  mask-image:linear-gradient(180deg,transparent,#000 22%,#000 74%,transparent),linear-gradient(90deg,transparent,#000 16% 84%,transparent);
  -webkit-mask-composite:source-in;mask-composite:intersect}
.scene-flow i{position:absolute;left:0;width:100%;height:1px;background:rgba(105,201,255,.15)}
.scene-flow i:nth-child(1){top:26%;transform:skewY(-6deg)}
.scene-flow i:nth-child(2){top:52%;transform:skewY(5deg)}
.scene-flow i:nth-child(3){top:74%;transform:skewY(-4deg)}
.scene-flow b{position:absolute;top:0;height:100%;width:18%;
  background:linear-gradient(90deg,transparent,rgba(75,184,255,.5),rgba(214,248,255,.8),transparent);
  box-shadow:0 0 10px rgba(46,143,240,.6);opacity:.7;animation:scene-sweep 9s linear infinite}
.scene-flow b:nth-of-type(2){animation-delay:-3.1s;opacity:.72}
.scene-flow b:nth-of-type(3){animation-delay:-6.2s;opacity:.5}
@keyframes scene-sweep{from{left:-20%}to{left:102%}}
.scene-floor{position:absolute;left:50%;top:74%;width:0;height:0;perspective:1500px}
.scene-floor-grid{position:absolute;width:3400px;height:3400px;margin:-1700px 0 0 -1700px;
  transform:rotateX(78deg) translateZ(-40px);
  background:
    repeating-linear-gradient(90deg,rgba(96,196,255,.2) 0 1.5px,transparent 1.5px 104px),
    repeating-linear-gradient(0deg,rgba(96,196,255,.2) 0 1.5px,transparent 1.5px 104px);
  -webkit-mask-image:radial-gradient(closest-side,#000 6%,rgba(0,0,0,.55) 34%,transparent 66%);
  mask-image:radial-gradient(closest-side,#000 6%,rgba(0,0,0,.55) 34%,transparent 66%)}
.scene-floor-grid:after{content:"";position:absolute;inset:0;
  background:radial-gradient(closest-side,rgba(40,150,240,.26),transparent 66%)}
.scene-horizon-glow{position:absolute;left:0;right:0;top:44%;height:26%;
  background:radial-gradient(60% 100% at 50% 100%,rgba(60,170,240,.2),transparent 72%);filter:blur(30px)}
.scene-horizon{position:absolute;left:0;right:0;top:58%;height:2px;
  background:linear-gradient(90deg,transparent,rgba(95,216,255,.42) 26%,rgba(190,240,255,.75) 50%,rgba(95,216,255,.42) 74%,transparent);
  filter:blur(2px);opacity:.55}
.scene-fog{position:absolute;left:0;right:0;bottom:2%;height:320px;
  background:radial-gradient(48% 100% at 50% 100%,rgba(66,150,220,.2),transparent 70%);filter:blur(40px)}
.scene-spot{position:absolute;top:-200px;width:760px;height:700px;
  background:radial-gradient(ellipse 34% 70% at 50% 0,rgba(150,225,255,.1),rgba(90,180,240,.03) 54%,transparent 76%);
  filter:blur(64px);clip-path:polygon(46% 0,54% 0,70% 100%,30% 100%);opacity:.3}
.scene-spot-left{left:2%;margin-left:-260px;opacity:.22}
.scene-spot-center{left:50%;margin-left:-400px;opacity:.32}
.scene-spot-right{right:2%;margin-right:-260px;opacity:.22}
.scene-particles{position:absolute;inset:0}
.scene-particles i{position:absolute;border-radius:50%;background:rgba(141,229,255,.55);
  box-shadow:0 0 8px rgba(95,216,255,.8);animation:scene-drift 12s ease-in-out infinite}
@keyframes scene-drift{0%,100%{transform:translateY(0);opacity:.15}50%{transform:translateY(-26px);opacity:.7}}
.scene-vignette{position:absolute;inset:0;
  background:radial-gradient(128% 92% at 50% 42%,transparent 26%,rgba(2,5,12,.5) 66%,rgba(1,3,9,.95) 100%)}
@media (prefers-reduced-motion:reduce){
  .scene-flow b,.scene-particles i{animation:none!important}
}
</style>
