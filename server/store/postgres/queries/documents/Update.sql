UPDATE documents
SET updated_at=$1, title=$2, date_month=$3, media_path=$4, duplicate_of=$5, additional_context=$6, linked_contexts=$7, series=$8
WHERE id=$9;
