import { describe, expect, it } from 'vitest'
import { LineOccupancy, walkDataEdge, type DataLine } from './dataEdge'

/** A line 1..total with the given cells filled and nothing hidden. */
const line=(total:number,filledCells:number[],hidden:number[]=[])=>{
  const filled=new Set(filledCells),skip=new Set(hidden)
  const step=(direction:1|-1)=>(index:number)=>{let next=index+direction;while(next>=1&&next<=total&&skip.has(next))next+=direction;return next<1||next>total?index:next}
  return {down:step(1),up:step(-1),filled:(index:number)=>filled.has(index)}
}
const range=(start:number,end:number)=>Array.from({length:end-start+1},(_,offset)=>start+offset)

describe('walkDataEdge', () => {
  it('runs to the last cell of the block it starts in', () => {
    const column=line(100,range(1,40))
    expect(walkDataEdge(1,column.down,column.filled)).toEqual({target:40})
    expect(walkDataEdge(40,column.up,column.filled)).toEqual({target:1})
  })

  it('jumps a gap to the next block, the way spreadsheets do', () => {
    const column=line(100,[...range(1,10),...range(20,30)])
    // From the block's last cell the next press lands on the next block.
    expect(walkDataEdge(10,column.down,column.filled)).toEqual({target:20})
    // From an empty cell it lands on the first filled one, not before it.
    expect(walkDataEdge(14,column.down,column.filled)).toEqual({target:20})
  })

  it('goes to the edge of the sheet when nothing follows', () => {
    const column=line(100,range(1,10))
    expect(walkDataEdge(10,column.down,column.filled)).toEqual({target:100})
    expect(walkDataEdge(50,column.down,column.filled)).toEqual({target:100})
    expect(walkDataEdge(100,column.down,column.filled)).toEqual({target:100})
  })

  it('steps over hidden rows without stopping on them', () => {
    // Row 5 is empty but hidden by a filter, so the block reads as 1..10.
    const column=line(100,[...range(1,4),...range(6,10)],[5])
    expect(walkDataEdge(1,column.down,column.filled)).toEqual({target:10})
  })

  it('asks for the cell it cannot see instead of calling it empty', () => {
    // Only rows 1..30 are loaded, as on screen; the block goes on below.
    const known=(index:number)=>index<=30?true:undefined
    expect(walkDataEdge(1,line(200_000,[]).down,known)).toEqual({unknown:31})
  })
})

describe('LineOccupancy', () => {
  it('answers from what the server covered and leaves the rest unknown', () => {
    const occupancy=new LineOccupancy()
    const answer:DataLine={direction:'down',from:1,through:4160,runs:[[1,50],[60,60]],limit:1_048_576}
    occupancy.add(answer)
    expect(occupancy.filled(1)).toBe(true)
    expect(occupancy.filled(50)).toBe(true)
    expect(occupancy.filled(51)).toBe(false)
    expect(occupancy.filled(60)).toBe(true)
    expect(occupancy.filled(4161)).toBeUndefined()
  })

  it('finishes a 50,000-row column in a few server answers', () => {
    // The server stops each answer at a chunk boundary; the walk asks again
    // from where the answer ended until it finds the block's end.
    const occupancy=new LineOccupancy(),column=line(200_000,[])
    const serve=(from:number):DataLine=>{const through=Math.min(from+4095,200_000);return {direction:'down',from,through,runs:[[from,Math.min(through,50_000)]].filter(([low,high])=>low<=high) as [number,number][],limit:200_000}}
    let walk=walkDataEdge(1,column.down,index=>occupancy.filled(index)),asked=0
    while('unknown' in walk){occupancy.add(serve(walk.unknown));asked+=1;walk=walkDataEdge(1,column.down,index=>occupancy.filled(index))}
    expect(walk).toEqual({target:50_000})
    expect(asked).toBeLessThan(20)
  })

  it('reads left and up answers the same way', () => {
    const occupancy=new LineOccupancy()
    occupancy.add({direction:'up',from:100,through:1,runs:[[5,9]],limit:1})
    expect(occupancy.filled(7)).toBe(true)
    expect(occupancy.filled(4)).toBe(false)
    expect(occupancy.filled(101)).toBeUndefined()
  })
})
