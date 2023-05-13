# !/bin/sh

runCmd="python3 main.py"

# time (
timeout 1s sh <<EOF
    echo "==== Program Output"
    echo "2" | $runCmd
EOF
# )

echo "==== Code: $?"