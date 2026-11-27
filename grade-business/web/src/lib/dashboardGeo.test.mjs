import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { districtHeatMap, projectDistrictBoundaries, readDistrictBoundaries } from './dashboardGeo.ts'

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
