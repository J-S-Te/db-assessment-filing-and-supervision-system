import { graphlib, layout, type EdgeLabel, type GraphLabel, type NodeLabel } from '@dagrejs/dagre'

interface TopologyRegistration {
  id: string
  identifier: string
  relatedEndpoint: string
  active: boolean
}

export const topologyNodeWidth = 156
export const topologyNodeHeight = 56

/**
 * 用途 交给 Dagre 布局当页有向拓扑，在用连接优先，保留并行及逆向连接。
 * 参数 registrations 为来自当前备案的拓扑登记，每条记录有独立标识。
 * 返回值 节点位置、每条连接的绘制点及整个布局尺寸。
 */
export function layoutTopology(registrations: readonly TopologyRegistration[]) {
  const graph = new graphlib.Graph<GraphLabel, NodeLabel, EdgeLabel>({ multigraph: true })
  graph.setGraph({
    rankdir: 'LR', acyclicer: 'greedy', nodesep: 40, ranksep: 88, marginx: 32, marginy: 32,
  })
  const names = new Set(registrations.flatMap(row => [row.identifier, row.relatedEndpoint]))
  for (const name of names) {
    graph.setNode(name, { width: topologyNodeWidth, height: topologyNodeHeight })
  }
  for (const row of registrations) {
    graph.setEdge(row.identifier, row.relatedEndpoint, { weight: row.active ? 2 : 1 }, row.id)
  }
  layout(graph)
  const nodes = Array.from(names, name => {
    const node = graph.node(name)!
    return { name, x: node.x! - topologyNodeWidth / 2, y: node.y! - topologyNodeHeight / 2 }
  })
  const edges = registrations.map(row => {
    const route = graph.edge(row.identifier, row.relatedEndpoint, row.id)!.points!
    const points = route.map(point => point.x.toFixed(2) + ',' + point.y.toFixed(2)).join(' ')
    return { id: row.id, active: row.active, points }
  })
  const { width = 0, height = 0 } = graph.graph()
  return { nodes, edges, width, height }
}
