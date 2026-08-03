import { describe, expect, it } from 'vitest'
import { createDimensionAxis } from './dimensionAxis'
import { clampDimensionSize, pointerRegion, resizeHandleAt, type GridGeometry } from './gridGeometry'

function geometry(overrides:Partial<GridGeometry>={}):GridGeometry{
  return{
    rowAxis:createDimensionAxis({total:1000,defaultSize:27}),
    columnAxis:createDimensionAxis({total:100,defaultSize:100}),
    scroll:{left:0,top:0},frozenRows:0,frozenColumns:0,headerWidth:46,headerHeight:27,
    ...overrides,
  }
}

describe('pointerRegion',()=>{
  it('classifies the corner, both headers and the grid body',()=>{
    const grid=geometry()
    expect(pointerRegion(grid,10,10)).toEqual({kind:'corner'})
    expect(pointerRegion(grid,146,10)).toEqual({kind:'column',index:2})
    expect(pointerRegion(grid,10,81)).toEqual({kind:'row',index:3})
    expect(pointerRegion(grid,146,81)).toEqual({kind:'cell',row:3,column:2})
  })

  it('accounts for scrolling',()=>{
    const grid=geometry({scroll:{left:300,top:270}})
    expect(pointerRegion(grid,56,37)).toEqual({kind:'cell',row:11,column:4})
  })
})

describe('resizeHandleAt',()=>{
  it('targets the column that ends at the boundary',()=>{
    const grid=geometry()
    expect(resizeHandleAt(grid,144,10,4)).toEqual({axis:'column',index:1})
    expect(resizeHandleAt(grid,148,10,4)).toEqual({axis:'column',index:1})
    expect(resizeHandleAt(grid,100,10,4)).toBeUndefined()
  })

  it('targets the row that ends at the boundary and ignores the body',()=>{
    const grid=geometry()
    expect(resizeHandleAt(grid,10,53,4)).toEqual({axis:'row',index:1})
    expect(resizeHandleAt(grid,10,40,4)).toBeUndefined()
    expect(resizeHandleAt(grid,200,200,4)).toBeUndefined()
  })

  it('skips hidden columns when grabbing a leading edge',()=>{
    const grid=geometry({columnAxis:createDimensionAxis({total:100,defaultSize:100,hiddenRanges:[{start:2,end:2}]})})
    expect(resizeHandleAt(grid,148,10,4)).toEqual({axis:'column',index:1})
  })
})

describe('clampDimensionSize',()=>{
  it('keeps sizes inside the server limits',()=>{
    expect(clampDimensionSize('column',12)).toBe(32)
    expect(clampDimensionSize('column',900)).toBe(600)
    expect(clampDimensionSize('row',10)).toBe(16)
    expect(clampDimensionSize('row',412.6)).toBe(400)
    expect(clampDimensionSize('row',42.4)).toBe(42)
  })
})
