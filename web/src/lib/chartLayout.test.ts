import { describe, expect, it } from 'vitest'
import { chartShape, shapeExtent, stackedBases } from './chartLayout'
import type { ChartSeries } from '../types'

const series=(name:string,values:number[]):ChartSeries=>
  ({name,points:values.map((value,index)=>({category:`C${index+1}`,value}))} as ChartSeries)

describe('chartShape', () => {
  it('draws a combination chart as bars plus a line for the last series', () => {
    const shape=chartShape('combo',[series('매출',[1,2]),series('비용',[1,1]),series('이익률',[0.1,0.2])],true)
    expect(shape.bars.map(item=>item.name)).toEqual(['매출','비용'])
    expect(shape.lines.map(item=>item.name)).toEqual(['이익률'])
    expect(shape.secondary).toBe(true)
  })

  it('falls back to a line when a combination chart has one series', () => {
    const shape=chartShape('combo',[series('매출',[1,2])])
    expect(shape.bars).toEqual([])
    expect(shape.lines).toHaveLength(1)
    expect(shape.secondary).toBe(false)
  })

  it('marks the stacked and filled kinds', () => {
    expect(chartShape('stacked_bar',[series('a',[1])])).toMatchObject({stacked:true,filled:false})
    expect(chartShape('stacked_area',[series('a',[1])])).toMatchObject({stacked:true,filled:true})
    expect(chartShape('area',[series('a',[1])])).toMatchObject({stacked:false,filled:true})
    expect(chartShape('bar',[series('a',[1])]).bars).toHaveLength(1)
  })
})

describe('stackedBases', () => {
  it('puts each series on the sum of the ones before it', () => {
    const {bases,totals}=stackedBases([series('a',[10,20]),series('b',[5,5])],2)
    expect(bases[0]).toEqual([0,0])
    expect(bases[1]).toEqual([10,20])
    expect(totals).toEqual([15,25])
  })
})

describe('shapeExtent', () => {
  it('reaches the top of the stack, not the tallest single value', () => {
    expect(shapeExtent([series('a',[10,20]),series('b',[5,5])],true).max).toBe(25)
    expect(shapeExtent([series('a',[10,20]),series('b',[5,5])],false).max).toBe(20)
  })

  it('always includes the baseline and never collapses to a point', () => {
    expect(shapeExtent([series('a',[5,5])],false)).toEqual({min:0,max:5})
    expect(shapeExtent([],false)).toEqual({min:0,max:1})
  })
})
