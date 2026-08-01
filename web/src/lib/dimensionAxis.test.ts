import { describe,expect,it } from 'vitest'
import { axisIndexAtViewport,axisViewportPosition,createDimensionAxis } from './dimensionAxis'

describe('dimension axis',()=>{
  it('combines sparse sizes and hidden ranges without materializing every row',()=>{
    const axis=createDimensionAxis({total:10,defaultSize:20,sizes:[{index:2,size:40},{index:7,size:30}],hiddenRanges:[{start:3,end:5}],hiddenIndexes:[8]})
    expect(axis.sizeOf(2)).toBe(40)
    expect(axis.sizeOf(4)).toBe(0)
    expect(axis.offsetOf(6)).toBe(60)
    expect(axis.extent).toBe(150)
    expect(axis.indexAtOffset(61)).toBe(6)
    expect(axis.countVisible(1,10)).toBe(6)
    expect(axis.rangeSize(2,6)).toBe(60)
  })
  it('maps frozen and scrolling dimensions to viewport coordinates',()=>{
    const axis=createDimensionAxis({total:100,defaultSize:10})
    expect(axisViewportPosition(axis,2,50,2)).toBe(10)
    expect(axisViewportPosition(axis,8,50,2)).toBe(20)
    expect(axisIndexAtViewport(axis,15,50,2)).toBe(2)
    expect(axisIndexAtViewport(axis,25,50,2)).toBe(8)
  })
})
