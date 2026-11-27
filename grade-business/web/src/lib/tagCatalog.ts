export const tagCatalogPath = '/tag-catalog'
export const defaultTagColor = '#64748b'

export interface TagCatalogEntry {
  id: string
  name: string
  color: string
  active: boolean
  usageCount: number
  version: number
}
