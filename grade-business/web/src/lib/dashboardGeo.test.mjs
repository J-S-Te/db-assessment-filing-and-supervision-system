import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { districtHeatMap, projectDistrictBoundaries, projectRiskSandbox, readDistrictBoundaries } from './dashboardGeo.ts'

const geometry = '{"type":"Polygon","coordinates":[[[1,2],[3,2],[3,3],[1,2]]]}'
const multipolygon = '{"type":"MultiPolygon","coordinates":[[[[4,3],[5,3],[5,4],[4,3]]],[[[1,4],[2,4],[2,5],[1,4]]]]}'
const boundaries = `{"type":"FeatureCollection","features":[
  {"type":"Feature","properties":{"name":"测试一区"},"geometry":${geometry}},
  {"type":"Feature","properties":{"name":"测试二区"},"geometry":${multipolygon}}
]}`

test('衢州底图覆盖六个法定县级行政区并可生成地图标签', () => {
  const source = readFileSync(new URL('../assets/district-boundaries.geojson', import.meta.url), 'utf8')
  const { features, metadata } = JSON.parse(source)
  assert.equal(metadata.cityCode, '330800')
  assert.deepEqual(features.map(({ properties }) => [properties.code, properties.name]), [
    ['330802', '柯城区'], ['330803', '衢江区'], ['330822', '常山县'],
    ['330824', '开化县'], ['330825', '龙游县'], ['330881', '江山市'],
  ])
  const paths = projectDistrictBoundaries(readDistrictBoundaries(source))
  assert.equal(paths.length, 6)
  for (const path of paths) {
    assert.match(path.path, /^M/)
    assert.equal(path.label.length, 2)
    assert.ok(path.label.every(Number.isFinite))
  }
})

test('区县账号的地图只投影本辖区，市本级投影衢州六区县', () => {
  const source = readFileSync(new URL('../assets/district-boundaries.geojson', import.meta.url), 'utf8')
  const features = readDistrictBoundaries(source)
  const county = projectDistrictBoundaries(features, ['柯城区'])
  assert.deepEqual(county.map(({ district }) => district), ['柯城区'])
  assert.ok(county[0].label.every(Number.isFinite))
  assert.deepEqual(projectDistrictBoundaries(features, []), [])
  assert.equal(projectDistrictBoundaries(features,
    ['衢州市', '柯城区', '衢江区', '常山县', '开化县', '龙游县', '江山市']).length, 6)
})

test('按区县名精确匹配真实统计，多面区县共享一个计数，未匹配保留', () => {
  const rows = [
    { district: '测试一区', systems: 1, high: 2, medium: 1, low: 0 },
    { district: '测试二区', systems: 2, high: 1, medium: 0, low: 1 },
    { district: '测试三', systems: 3, high: 9, medium: 0, low: 0 },
    { district: '测试二', systems: 1, high: 0, medium: 1, low: 0 },
  ]
  const paths = projectDistrictBoundaries(readDistrictBoundaries(boundaries))
  const result = districtHeatMap(paths, rows, 'all')
  assert.deepEqual(result.shapes.map(item => [item.district, item.value, item.intensity]), [
    ['测试一区', 3, 'strong'], ['测试二区', 2, 'medium'],
  ])
  assert.deepEqual(result.unmatched, ['测试三', '测试二'])
  assert.equal(result.max, 3)
  assert.equal(result.shapes[1].path.match(/M/g).length, 2)
  const high = districtHeatMap(paths, [rows[0]], 'high')
  assert.equal(high.shapes[0].value, 2)
  assert.equal(high.shapes[1].value, undefined)
  assert.equal(high.shapes[1].intensity, 'unmatched')
  assert.equal(high.shapes[0].path, result.shapes[0].path)
})

test('拒绝重复区县、不闭合、多余坐标数和自带非 WGS84 坐标系', () => {
  for (const input of [
    boundaries.replace('测试二区', '测试一区'),
    boundaries.replace('[3,3],[1,2]', '[3,3],[1,3]'),
    boundaries.replace('[1,2]', '[1,2,3,4]'),
    boundaries.replace('[3,3]', '[3000,3]'),
    boundaries.replace('[3,3]', '[2,2]'),
    boundaries.replace('"features"', '"crs":{},"features"'),
    boundaries.replace('"MultiPolygon"', '"Point"'),
    boundaries + ' '.repeat(2 * 1024 * 1024),
  ]) assert.throws(() => readDistrictBoundaries(input))
})

test('风险沙盘按原型的 34 个东西模型单位投影真实底图', () => {
  const source = readFileSync(new URL('../assets/district-boundaries.geojson', import.meta.url), 'utf8')
  const { outlines, metersPerUnit } = projectRiskSandbox(readDistrictBoundaries(source))
  assert.equal(outlines.length, 6)
  assert.ok(outlines.every(row => row.label.every(Number.isFinite)))
  const x = outlines.flatMap(row => row.rings.flatMap(([outer]) => outer.map(([value]) => value)))
  assert.ok(Math.abs(Math.max(...x) - 17) < 1e-9)
  assert.ok(Math.abs(Math.min(...x) + 17) < 1e-9)
  assert.ok(metersPerUnit > 0)
})

test('沙盘对会话辖区先筛选再归一，单区县和全域使用同一模型尺度', () => {
  const source = readFileSync(new URL('../assets/district-boundaries.geojson', import.meta.url), 'utf8')
  const boundaries = readDistrictBoundaries(source)
  const visible = projectRiskSandbox(boundaries, ['衢州市', '龙游县'])
  assert.deepEqual(visible.outlines.map(row => row.district), ['龙游县'])
  const x = visible.outlines.flatMap(row => row.rings.flatMap(([outer]) => outer.map(point => point[0])))
  assert.ok(Math.abs(Math.max(...x) - Math.min(...x) - 34) < 1e-9)
  assert.ok(visible.metersPerUnit < projectRiskSandbox(boundaries).metersPerUnit)
  assert.deepEqual(projectRiskSandbox(boundaries, []).outlines, [])
})
