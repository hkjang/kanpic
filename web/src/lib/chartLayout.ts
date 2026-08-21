import type { ChartSeries } from '../types'

export type ChartShape={
  /** Series drawn as bars, in draw order. */
  bars:ChartSeries[]
  /** Series drawn as a line, in draw order. */
  lines:ChartSeries[]
  /** Bars stack on one another instead of standing side by side. */
  stacked:boolean
  /** Lines are filled down to the baseline. */
  filled:boolean
  /** The line series has its own scale on the right. */
  secondary:boolean
}

/**
 * Splits the series by how each one is drawn. A combination chart is bars for
 * everything except the last series, which becomes the line — that is the
 * shape a business report wants: amounts as columns, the ratio on top.
 */
export function chartShape(type:string,series:ChartSeries[],secondaryAxis=false):ChartShape{
  if(type==='combo'&&series.length>1){
    return {bars:series.slice(0,-1),lines:series.slice(-1),stacked:false,filled:false,secondary:secondaryAxis}
  }
  if(type==='combo')return {bars:[],lines:series,stacked:false,filled:false,secondary:false}
  if(type==='stacked_bar')return {bars:series,lines:[],stacked:true,filled:false,secondary:false}
  if(type==='stacked_area')return {bars:[],lines:series,stacked:true,filled:true,secondary:false}
  if(type==='bar'||type==='histogram')return {bars:series,lines:[],stacked:false,filled:false,secondary:false}
  if(type==='area')return {bars:[],lines:series,stacked:false,filled:true,secondary:false}
  if(type==='line')return {bars:[],lines:series,stacked:false,filled:false,secondary:false}
  return {bars:[],lines:[],stacked:false,filled:false,secondary:false}
}

/**
 * The running totals a stacked chart draws from: each series sits on the sum
 * of the ones before it, so a segment spans base to base+value.
 */
export function stackedBases(series:ChartSeries[],pointCount:number){
  const bases=series.map(()=>new Array<number>(pointCount).fill(0))
  const running=new Array<number>(pointCount).fill(0)
  series.forEach((item,seriesIndex)=>{
    for(let index=0;index<pointCount;index+=1){
      bases[seriesIndex][index]=running[index]
      running[index]+=item.points[index]?.value??0
    }
  })
  return {bases,totals:running}
}

/** The value range a set of series needs, honouring stacking. */
export function shapeExtent(series:ChartSeries[],stacked:boolean){
  const values:number[]=[]
  if(stacked&&series.length>0){
    const count=series[0].points.length
    const {totals}=stackedBases(series,count)
    values.push(...totals)
    // A stack of negatives still has to fit under the baseline.
    for(const item of series)for(const point of item.points)if(point.value!=null&&point.value<0)values.push(point.value)
  }else{
    for(const item of series)for(const point of item.points)if(point.value!=null)values.push(point.value)
  }
  if(values.length===0)return {min:0,max:1}
  let min=Math.min(0,...values),max=Math.max(0,...values)
  if(min===max){min-=1;max+=1}
  return {min,max}
}
