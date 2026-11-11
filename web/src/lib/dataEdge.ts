/**
 * Ctrl+arrow, the way spreadsheets do it.
 *
 * From a filled cell whose neighbour is filled too, the walk goes to the last
 * filled cell of that block. From anywhere else it goes to the next filled
 * cell, or to the edge of the sheet when there is none. Hidden rows and
 * columns are stepped over, never landed on.
 *
 * The grid only holds the rows on screen, so "filled" has three answers here:
 * yes, no, and not known yet. A walk that reaches a cell nobody has looked at
 * stops and says which cell it needs; the caller fetches that stretch from the
 * server and walks again. Treating the unknown as empty is what used to stop
 * Ctrl+Down in the middle of a long column.
 */
export type EdgeWalk={target:number}|{unknown:number}

export function walkDataEdge(start:number,next:(index:number)=>number,filled:(index:number)=>boolean|undefined):EdgeWalk{
  const first=next(start)
  if(first===start)return {target:start}
  const here=filled(start)
  if(here===undefined)return {unknown:start}
  const ahead=filled(first)
  if(ahead===undefined)return {unknown:first}
  if(here&&ahead){
    let current=first
    for(;;){
      const following=next(current)
      if(following===current)return {target:current}
      const known=filled(following)
      if(known===undefined)return {unknown:following}
      if(!known)return {target:current}
      current=following
    }
  }
  let current=first
  for(;;){
    const known=filled(current)
    if(known===undefined)return {unknown:current}
    if(known)return {target:current}
    const following=next(current)
    if(following===current)return {target:current}
    current=following
  }
}

/** What the server reports about one row or column: GET …/cells/{A1}/edge. */
export type DataLine={direction:'up'|'down'|'left'|'right';from:number;through:number;runs:[number,number][];limit:number}

/**
 * The stretches of one line the server has described so far. A covered index
 * is filled when it sits in a run and empty otherwise; an index outside every
 * covered span is unknown.
 */
export class LineOccupancy{
  private spans:[number,number][]=[]
  private runs:[number,number][]=[]

  add(line:DataLine){
    this.spans.push([Math.min(line.from,line.through),Math.max(line.from,line.through)])
    this.runs.push(...line.runs)
    this.runs.sort((left,right)=>left[0]-right[0])
  }

  filled(index:number):boolean|undefined{
    if(!this.spans.some(([low,high])=>index>=low&&index<=high))return undefined
    // A sparse column reports a run per filled cell, so the lookup halves
    // rather than scanning: a walk asks once per step.
    let low=0,high=this.runs.length-1
    while(low<=high){
      const middle=(low+high)>>1,[start,end]=this.runs[middle]
      if(index<start)high=middle-1
      else if(index>end)low=middle+1
      else return true
    }
    return false
  }
}
