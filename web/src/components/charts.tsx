import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import dayjs from 'dayjs'
import { useChartTheme } from '../hooks/useChartTheme'

function useTooltipStyle() {
  const c = useChartTheme()
  return {
    contentStyle: {
      background: c.tooltipBg,
      border: `1px solid ${c.tooltipBorder}`,
      borderRadius: 6,
      color: c.textPrimary,
      fontSize: 12,
    },
    labelStyle: { color: c.textPrimary, fontWeight: 600 },
    itemStyle: { color: c.textPrimary },
  }
}

export interface NameValue {
  name: string
  value: number
  color?: string
}

/** 水平長條圖（單一數列，量值比較）。 */
export function HBarChart({ data, height, unit = '台', onClick }: { data: NameValue[]; height?: number; unit?: string; onClick?: (name: string) => void }) {
  const c = useChartTheme()
  const tt = useTooltipStyle()
  const h = height ?? Math.max(120, data.length * 34 + 24)
  return (
    <ResponsiveContainer width="100%" height={h}>
      <BarChart data={data} layout="vertical" margin={{ top: 4, right: 40, bottom: 4, left: 8 }} barCategoryGap={6}>
        <CartesianGrid horizontal={false} stroke={c.grid} strokeDasharray="3 3" />
        <XAxis type="number" allowDecimals={false} tick={{ fill: c.text, fontSize: 12 }} axisLine={false} tickLine={false} />
        <YAxis type="category" dataKey="name" width={130} tick={{ fill: c.text, fontSize: 12 }} axisLine={false} tickLine={false} />
        <Tooltip {...tt} cursor={{ fill: c.grid, opacity: 0.4 }} formatter={(v) => [`${String(v)} ${unit}`, '數量']} />
        <Bar
          dataKey="value"
          radius={[0, 4, 4, 0]}
          maxBarSize={22}
          label={{ position: 'right', fill: c.text, fontSize: 12 }}
          onClick={onClick ? (d: { payload?: NameValue }) => d.payload && onClick(d.payload.name) : undefined}
          style={onClick ? { cursor: 'pointer' } : undefined}
        >
          {data.map((d) => (
            <Cell key={d.name} fill={d.color ?? c.series[0]} />
          ))}
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  )
}

/** 環圈圖（少量類別的組成），附圖例與數值。 */
export function DonutChart({ data, height = 220, unit = '台', onClick }: { data: NameValue[]; height?: number; unit?: string; onClick?: (name: string) => void }) {
  const c = useChartTheme()
  const tt = useTooltipStyle()
  const total = data.reduce((a, b) => a + b.value, 0)
  const shown = data.filter((d) => d.value > 0)
  return (
    <ResponsiveContainer width="100%" height={height}>
      <PieChart>
        <Pie
          data={shown}
          dataKey="value"
          nameKey="name"
          innerRadius="58%"
          outerRadius="85%"
          paddingAngle={shown.length > 1 ? 1 : 0}
          stroke={c.surface}
          strokeWidth={2}
          onClick={onClick ? (d: { name?: string }) => d.name && onClick(d.name) : undefined}
          style={onClick ? { cursor: 'pointer' } : undefined}
          isAnimationActive={false}
        >
          {shown.map((d, i) => (
            <Cell key={d.name} fill={d.color ?? c.series[i % c.series.length]} />
          ))}
        </Pie>
        <Tooltip
          {...tt}
          formatter={(v, n) => [`${String(v)} ${unit}（${total ? Math.round((Number(v) / total) * 100) : 0}%）`, String(n)]}
        />
        <Legend
          verticalAlign="middle"
          align="right"
          layout="vertical"
          iconType="circle"
          formatter={(value: string) => {
            const d = data.find((x) => x.name === value)
            return (
              <span style={{ color: c.textPrimary, fontSize: 13 }}>
                {value} <span style={{ color: c.text }}>{d?.value ?? 0}</span>
              </span>
            )
          }}
        />
      </PieChart>
    </ResponsiveContainer>
  )
}

export interface LineSeries {
  key: string
  name: string
  color?: string
}

/** 時間序列折線圖（同一單位的數列共用單一 Y 軸）。 */
export function TimeLineChart<T extends object>({
  data,
  xKey,
  series,
  height = 260,
  unit = '%',
  yDomain,
  timeFormat = 'MM-DD HH:mm',
}: {
  data: T[]
  xKey: keyof T & string
  series: LineSeries[]
  height?: number
  unit?: string
  yDomain?: [number | 'auto', number | 'auto']
  timeFormat?: string
}) {
  const c = useChartTheme()
  const tt = useTooltipStyle()
  return (
    <ResponsiveContainer width="100%" height={height}>
      <LineChart data={data} margin={{ top: 8, right: 16, bottom: 0, left: 0 }}>
        <CartesianGrid vertical={false} stroke={c.grid} strokeDasharray="3 3" />
        <XAxis
          dataKey={xKey as string}
          tickFormatter={(v: string) => dayjs(v).format(timeFormat)}
          tick={{ fill: c.text, fontSize: 11 }}
          axisLine={{ stroke: c.grid }}
          tickLine={false}
          minTickGap={40}
        />
        <YAxis
          domain={yDomain ?? [0, 'auto']}
          tick={{ fill: c.text, fontSize: 11 }}
          axisLine={false}
          tickLine={false}
          width={44}
          tickFormatter={(v: number) => `${v}${unit === '%' ? '%' : ''}`}
        />
        <Tooltip
          {...tt}
          labelFormatter={(v) => dayjs(String(v)).format('YYYY-MM-DD HH:mm')}
          formatter={(v, n) => [`${typeof v === 'number' ? v.toFixed(1) : String(v)}${unit === '%' ? '%' : ` ${unit}`}`, String(n)]}
          cursor={{ stroke: c.text, strokeDasharray: '3 3' }}
        />
        {series.length > 1 && <Legend iconType="plainline" wrapperStyle={{ fontSize: 12, color: c.textPrimary }} />}
        {series.map((s, i) => (
          <Line
            key={s.key}
            type="monotone"
            dataKey={s.key}
            name={s.name}
            stroke={s.color ?? c.series[i]}
            strokeWidth={2}
            dot={data.length < 40 ? { r: 3, strokeWidth: 0, fill: s.color ?? c.series[i] } : false}
            activeDot={{ r: 5, stroke: c.surface, strokeWidth: 2 }}
            isAnimationActive={false}
            connectNulls
          />
        ))}
      </LineChart>
    </ResponsiveContainer>
  )
}
