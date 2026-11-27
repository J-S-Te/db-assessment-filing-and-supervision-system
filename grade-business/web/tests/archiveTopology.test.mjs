import assert from 'node:assert/strict'
import { test } from 'node:test'
import { layoutTopology } from '../src/lib/archiveTopology.ts'

test('拓扑保留登记方向和停用状态，共用同名节点', () => {
  const graph = layoutTopology([
    { id: 'one', identifier: '入口', relatedEndpoint: '应用', active: true },
    { id: 'two', identifier: '应用', relatedEndpoint: '数据', active: true },
    { id: 'three', identifier: '应用', relatedEndpoint: '入口', active: false },
  ])
  assert.equal(graph.nodes.length, 3)
  assert.deepEqual(graph.edges.map(({ id, active }) => [id, active]), [
    ['one', true], ['two', true], ['three', false],
  ])
  assert.deepEqual(graph.edges.map(edge => {
    const x = edge.points.split(' ').map(point => Number(point.split(',')[0]))
    return Math.sign(x.at(-1) - x[0])
  }), [1, 1, -1])
})

test('无登记时拓扑为空，断开的连接及循环仍各自拥有节点', () => {
  assert.deepEqual(layoutTopology([]).nodes, [])
  const graph = layoutTopology([
    { id: 'one', identifier: '环节点甲', relatedEndpoint: '环节点乙', active: true },
    { id: 'two', identifier: '环节点乙', relatedEndpoint: '环节点甲', active: true },
    { id: 'three', identifier: '另一端', relatedEndpoint: '离线区', active: true },
  ])
  const positions = new Set(graph.nodes.map(({ x, y }) => [x, y].join(',')))
  assert.equal(positions.size, 4)
  assert.equal(graph.edges.length, 3)
})

test('拓扑布局为并行和逆向连接保留各自的路径', () => {
  const graph = layoutTopology([
    { id: 'a', identifier: '业务网关', relatedEndpoint: '业务服务', active: true },
    { id: 'b', identifier: '业务网关', relatedEndpoint: '业务服务', active: false },
    { id: 'c', identifier: '业务服务', relatedEndpoint: '业务网关', active: true },
  ])
  assert.deepEqual(graph.nodes.map(node => node.name).sort(), ['业务服务', '业务网关'])
  assert.deepEqual(graph.edges.map(({ id, active }) => [id, active]), [
    ['a', true], ['b', false], ['c', true],
  ])
  const routes = graph.edges.map(edge => edge.points)
  assert.equal(new Set(routes).size, 3)
  assert.ok(routes.every(route => /^-?[\d.]+,-?[\d.]+( -?[\d.]+,-?[\d.]+)+$/.test(route)))
  assert.ok(Number.isFinite(graph.width) && Number.isFinite(graph.height))
})
